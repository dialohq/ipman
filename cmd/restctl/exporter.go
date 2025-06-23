package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/plan9better/goviciclient"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/strongswan/govici/vici"
)

type StrongswanCollector struct {
	namespace string

	ikeCnt           *prometheus.Desc
	ikeConnCnt       *prometheus.Desc
	ikeVersion       *prometheus.Desc
	ikeState         *prometheus.Desc
	ikeInitiator     *prometheus.Desc
	ikeNatRemote     *prometheus.Desc
	ikeNatFake       *prometheus.Desc
	ikeEncKeysize    *prometheus.Desc
	ikeIntegKeysize  *prometheus.Desc
	ikeEstablishSecs *prometheus.Desc
	ikeRekeySecs     *prometheus.Desc
	ikeReauthSecs    *prometheus.Desc
	ikeChildren      *prometheus.Desc

	saState              *prometheus.Desc
	saEncap              *prometheus.Desc
	saEncKeysize         *prometheus.Desc
	saIntegKeysize       *prometheus.Desc
	saBytesIn            *prometheus.Desc
	saPacketsIn          *prometheus.Desc
	saLastInSecs         *prometheus.Desc
	saBytesOut           *prometheus.Desc
	saPacketsOut         *prometheus.Desc
	saLastOutSecs        *prometheus.Desc
	saEstablishSecs      *prometheus.Desc
	saRekeySecs          *prometheus.Desc
	saLifetimeSecs       *prometheus.Desc
	connectionsLoadedCnt *prometheus.Desc
	childrenLoadedCnt    *prometheus.Desc
	connectionsLoaded    *prometheus.Desc
	childrenLoaded       *prometheus.Desc
}

func NewStrongswanCollector() *StrongswanCollector {
	ns := "strongswan_"
	return &StrongswanCollector{
		namespace: ns,

		ikeCnt: prometheus.NewDesc(
			ns+"number_of_known_ikes",
			"Number of known IKEs",
			nil, nil,
		),
		ikeConnCnt: prometheus.NewDesc(
			ns+"number_of_ikes_connected",
			"Number of temporary connected IKEs",
			nil, nil,
		),
		connectionsLoadedCnt: prometheus.NewDesc(
			ns+"number_of_connections_loaded",
			"connections loaded into the daemon",
			nil, nil,
		),
		connectionsLoaded: prometheus.NewDesc(
			ns+"connections_loaded",
			"total number of connections loaded into the daemon",
			[]string{"conn_name"}, nil,
		),
		childrenLoaded: prometheus.NewDesc(
			ns+"children_loaded",
			"children loaded into the daemon",
			[]string{"child_name", "conn_name"}, nil,
		),
		childrenLoadedCnt: prometheus.NewDesc(
			ns+"number_of_children_loaded",
			"total number of children loaded into the daemon",
			nil, nil,
		),
		ikeVersion: prometheus.NewDesc(
			ns+"ike_version",
			"Version Number of this IKE",
			[]string{"name", "uniqueid"}, nil,
		),
		ikeState: prometheus.NewDesc(
			ns+"ike_state",
			"Status of this IKE",
			[]string{"name", "uniqueid"}, nil,
		),
		ikeInitiator: prometheus.NewDesc(
			ns+"ike_initiator",
			"Flag if the server is the initiator for this connection",
			[]string{"name", "uniqueid"}, nil,
		),
		ikeNatRemote: prometheus.NewDesc(
			ns+"ike_nat_remote",
			"Flag if the remote server is behind nat",
			[]string{"name", "uniqueid"}, nil,
		),
		ikeNatFake: prometheus.NewDesc(
			ns+"ike_nat_fake",
			"Flag if the NAT is faked (to float to 4500)",
			[]string{"name", "uniqueid"}, nil,
		),
		ikeEncKeysize: prometheus.NewDesc(
			ns+"ike_encryption_keysize",
			"Keysize of the encryption algorithm",
			[]string{"name", "uniqueid", "algorithm", "dh_group"}, nil,
		),
		ikeIntegKeysize: prometheus.NewDesc(
			ns+"ike_integrity_keysize",
			"Keysize of the integrity algorithm",
			[]string{"name", "uniqueid", "algorithm", "dh_group"}, nil,
		),
		ikeEstablishSecs: prometheus.NewDesc(
			ns+"ike_established_second",
			"Seconds since the IKE was established",
			[]string{"name", "uniqueid"}, nil,
		),
		ikeRekeySecs: prometheus.NewDesc(
			ns+"ike_rekey_second",
			"Second count until the IKE will be rekeyed",
			[]string{"name", "uniqueid"}, nil,
		),
		ikeReauthSecs: prometheus.NewDesc(
			ns+"ike_reauth_second",
			"Second count until the IKE will be reauthed",
			[]string{"name", "uniqueid"}, nil,
		),
		ikeChildren: prometheus.NewDesc(
			ns+"ike_children",
			"Count of children of this IKE",
			[]string{"name", "uniqueid"}, nil,
		),

		saState: prometheus.NewDesc(
			ns+"sa_state",
			"Status of this child sa",
			[]string{"ike_name", "ike_id", "child_name", "child_id", "localTS", "remoteTS"}, nil,
		),
		saEncap: prometheus.NewDesc(
			ns+"sa_encap",
			"Forced Encapsulation in UDP Packets",
			[]string{"ike_name", "ike_id", "child_name", "child_id"}, nil,
		),
		saEncKeysize: prometheus.NewDesc(
			ns+"sa_encryption_keysize",
			"Keysize of the encryption algorithm",
			[]string{"ike_name", "ike_id", "child_name", "child_id", "algorithm", "dh_group"}, nil,
		),
		saIntegKeysize: prometheus.NewDesc(
			ns+"sa_integrity_keysize",
			"Keysize of the integrity algorithm",
			[]string{"ike_name", "ike_id", "child_name", "child_id", "algorithm", "dh_group"}, nil,
		),
		saBytesIn: prometheus.NewDesc(
			ns+"sa_bytes_inbound",
			"Number of bytes coming to the local server",
			[]string{"ike_name", "ike_id", "child_name", "child_id", "localTS", "remoteTS"}, nil,
		),
		saPacketsIn: prometheus.NewDesc(
			ns+"sa_packets_inbound",
			"Number of packets coming to the local server",
			[]string{"ike_name", "ike_id", "child_name", "child_id", "localTS", "remoteTS"}, nil,
		),
		saLastInSecs: prometheus.NewDesc(
			ns+"sa_last_inbound_seconds",
			"Number of seconds since the last inbound packet was received",
			[]string{"ike_name", "ike_id", "child_name", "child_id", "localTS", "remoteTS"}, nil,
		),
		saBytesOut: prometheus.NewDesc(
			ns+"sa_bytes_outbound",
			"Number of bytes going to the remote server",
			[]string{"ike_name", "ike_id", "child_name", "child_id", "localTS", "remoteTS"}, nil,
		),
		saPacketsOut: prometheus.NewDesc(
			ns+"sa_packets_outbound",
			"Number of packets going to the remote server",
			[]string{"ike_name", "ike_id", "child_name", "child_id", "localTS", "remoteTS"}, nil,
		),
		saLastOutSecs: prometheus.NewDesc(
			ns+"sa_last_outbound_seconds",
			"Number of seconds since the last outbound packet was sent",
			[]string{"ike_name", "ike_id", "child_name", "child_id", "localTS", "remoteTS"}, nil,
		),
		saEstablishSecs: prometheus.NewDesc(
			ns+"sa_established_second",
			"Seconds since the child SA was established",
			[]string{"ike_name", "ike_id", "child_name", "child_id"}, nil,
		),
		saRekeySecs: prometheus.NewDesc(
			ns+"sa_rekey_second",
			"Second count until the child SA will be rekeyed",
			[]string{"ike_name", "ike_id", "child_name", "child_id"}, nil,
		),
		saLifetimeSecs: prometheus.NewDesc(
			ns+"sa_lifetime_second",
			"Second count until the lifetime expires",
			[]string{"ike_name", "ike_id", "child_name", "child_id"}, nil,
		),
	}
}

func listSAs() ([]LoadedIKE, error) {
	s, err := vici.NewSession()
	if err != nil {
		log.Printf("Error Connecting to vici: %s", err)
		return nil, err
	}
	defer s.Close()

	var retVar []LoadedIKE
	msgs, err := s.StreamedCommandRequest("list-sas", "list-sa", nil)
	if err != nil {
		return retVar, err
	}
	for _, m := range msgs.Messages() { // <- Directly iterate over msgs
		if e := m.Err(); e != nil {
			//ignoring this error
			continue
		}
		for _, k := range m.Keys() {
			inbound := m.Get(k).(*vici.Message)
			var ike LoadedIKE
			if e := vici.UnmarshalMessage(inbound, &ike); e != nil {
				//ignoring this marshal/unmarshal error!
				continue
			}
			ike.Name = k
			retVar = append(retVar, ike)
		}
	}

	return retVar, nil
}
func listConns() ([]goviciclient.ConnectionsMap, error) {
	client, err := goviciclient.NewViciClient(nil)
	if err != nil {
		fmt.Println("Error creating govici client", err)
		return nil, err
	}
	conns, err := client.ListConns(nil)
	if err != nil {
		fmt.Println("Error listing conns", err)
		return nil, err
	}
	return conns, nil
}
func (c *StrongswanCollector) init() {
	prometheus.MustRegister(c)
}

func (c *StrongswanCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.ikeCnt
	ch <- c.ikeConnCnt
	ch <- c.ikeVersion
	ch <- c.ikeState
	ch <- c.ikeInitiator
	ch <- c.ikeNatRemote
	ch <- c.ikeNatFake
	ch <- c.ikeEncKeysize
	ch <- c.ikeIntegKeysize
	ch <- c.ikeEstablishSecs
	ch <- c.ikeRekeySecs
	ch <- c.ikeReauthSecs
	ch <- c.ikeChildren

	ch <- c.saState
	ch <- c.saEncap
	ch <- c.saEncKeysize
	ch <- c.saIntegKeysize
	ch <- c.saBytesIn
	ch <- c.saPacketsIn
	ch <- c.saLastInSecs
	ch <- c.saBytesOut
	ch <- c.saPacketsOut
	ch <- c.saLastOutSecs
	ch <- c.saEstablishSecs
	ch <- c.saRekeySecs
	ch <- c.saLifetimeSecs

	ch <- c.connectionsLoadedCnt
	ch <- c.connectionsLoaded
	ch <- c.childrenLoadedCnt
	ch <- c.childrenLoaded

}
func (c *StrongswanCollector) Collect(ch chan<- prometheus.Metric) {
	conns, err := listConns()
	if err != nil {
		fmt.Println("Error listing conns", err)
		ch <- prometheus.MustNewConstMetric(
			c.connectionsLoadedCnt,
			prometheus.GaugeValue,
			float64(0),
		)
		return
	}
	for _, cm := range conns {
		for k, v := range cm {
			ch <- prometheus.MustNewConstMetric(
				c.connectionsLoaded,
				prometheus.GaugeValue,
				float64(1),
				k,
			)
			for n := range v.Children {
				ch <- prometheus.MustNewConstMetric(
					c.childrenLoaded,
					prometheus.GaugeValue,
					float64(1),
					n, k,
				)
			}
		}
	}
	ch <- prometheus.MustNewConstMetric(
		c.connectionsLoadedCnt,
		prometheus.GaugeValue,
		float64(len(conns)),
	)
	sas, err := listSAs()
	if err != nil {
		ch <- prometheus.MustNewConstMetric(
			c.ikeConnCnt,          //Description
			prometheus.GaugeValue, //Type
			float64(0),            //Value
		)
		return
	}
	ch <- prometheus.MustNewConstMetric(
		c.ikeConnCnt,          //Description
		prometheus.GaugeValue, //Type
		float64(len(sas)),     //Value
	)
	for _, v := range sas {
		c.collectIkeMetrics(v, ch)
		for _, child := range v.Children {
			c.collectSaMetrics(v.Name, v.UniqueId, child, ch)
		}
	}
}
func (c *StrongswanCollector) collectIkeMetrics(d LoadedIKE, ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(
		c.ikeVersion,          //Description
		prometheus.GaugeValue, //Type
		float64(d.Version),    //Value
		d.Name, d.UniqueId,    //Labels
	)

	state := 0
	if d.State == "ESTABLISHED" {
		state = 1
	}

	ch <- prometheus.MustNewConstMetric(
		c.ikeState,            //Description
		prometheus.GaugeValue, //Type
		float64(state),        //Value
		d.Name, d.UniqueId,    //Labels
	)

	ch <- prometheus.MustNewConstMetric(
		c.ikeInitiator,                      //Description
		prometheus.GaugeValue,               //Type
		float64(viciBoolToInt(d.Initiator)), //Value
		d.Name, d.UniqueId,                  //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.ikeNatRemote,                      //Description
		prometheus.GaugeValue,               //Type
		float64(viciBoolToInt(d.NatRemote)), //Value
		d.Name, d.UniqueId,                  //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.ikeNatFake,                      //Description
		prometheus.GaugeValue,             //Type
		float64(viciBoolToInt(d.NatFake)), //Value
		d.Name, d.UniqueId,                //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.ikeEncKeysize,                         //Description
		prometheus.GaugeValue,                   //Type
		float64(d.EncKey),                       //Value
		d.Name, d.UniqueId, d.EncAlg, d.DHGroup, //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.ikeIntegKeysize,                         //Description
		prometheus.GaugeValue,                     //Type
		float64(d.IntegKey),                       //Value
		d.Name, d.UniqueId, d.IntegAlg, d.DHGroup, //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.ikeEstablishSecs,      //Description
		prometheus.GaugeValue,   //Type
		float64(d.EstablishSec), //Value
		d.Name, d.UniqueId,      //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.ikeRekeySecs,        //Description
		prometheus.GaugeValue, //Type
		float64(d.RekeySec),   //Value
		d.Name, d.UniqueId,    //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.ikeReauthSecs,       //Description
		prometheus.GaugeValue, //Type
		float64(d.ReauthSec),  //Value
		d.Name, d.UniqueId,    //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.ikeChildren,
		prometheus.GaugeValue,    //Type
		float64(len(d.Children)), //Value
		d.Name, d.UniqueId,       //Labels
	)
}
func (c *StrongswanCollector) collectSaMetrics(name string, uniqueId string, d LoadedChild, ch chan<- prometheus.Metric) {
	state := 0
	if d.State == "ESTABLISHED" {
		state = 1
	}
	ch <- prometheus.MustNewConstMetric(
		c.saState,                                                                                       //Description
		prometheus.GaugeValue,                                                                           //Type
		float64(state),                                                                                  //Value
		name, uniqueId, d.Name, d.UniqueId, strings.Join(d.LocalTS, ";"), strings.Join(d.RemoteTS, ";"), //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.saEncap,                          //Description
		prometheus.GaugeValue,              //Type
		float64(viciBoolToInt(d.Encap)),    //Value
		name, uniqueId, d.Name, d.UniqueId, //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.saEncKeysize,                                          //Description
		prometheus.GaugeValue,                                   //Type
		float64(d.EncKey),                                       //Value
		name, uniqueId, d.Name, d.UniqueId, d.EncAlg, d.DHGroup, //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.saIntegKeysize,                                          //Description
		prometheus.GaugeValue,                                     //Type
		float64(d.IntegKey),                                       //Value
		name, uniqueId, d.Name, d.UniqueId, d.IntegAlg, d.DHGroup, //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.saBytesIn,                                                                                     //Description
		prometheus.GaugeValue,                                                                           //Type
		float64(d.BytesIn),                                                                              //Value
		name, uniqueId, d.Name, d.UniqueId, strings.Join(d.LocalTS, ";"), strings.Join(d.RemoteTS, ";"), //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.saPacketsIn,                                                                                   //Description
		prometheus.GaugeValue,                                                                           //Type
		float64(d.PacketsIn),                                                                            //Value
		name, uniqueId, d.Name, d.UniqueId, strings.Join(d.LocalTS, ";"), strings.Join(d.RemoteTS, ";"), //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.saLastInSecs,                                                                                  //Description
		prometheus.GaugeValue,                                                                           //Type
		float64(d.LastInSec),                                                                            //Value
		name, uniqueId, d.Name, d.UniqueId, strings.Join(d.LocalTS, ";"), strings.Join(d.RemoteTS, ";"), //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.saBytesOut,                                                                                    //Description
		prometheus.GaugeValue,                                                                           //Type
		float64(d.BytesOut),                                                                             //Value
		name, uniqueId, d.Name, d.UniqueId, strings.Join(d.LocalTS, ";"), strings.Join(d.RemoteTS, ";"), //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.saPacketsOut,                                                                                  //Description
		prometheus.GaugeValue,                                                                           //Type
		float64(d.PacketsOut),                                                                           //Value
		name, uniqueId, d.Name, d.UniqueId, strings.Join(d.LocalTS, ";"), strings.Join(d.RemoteTS, ";"), //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.saLastOutSecs,                                                                                 //Description
		prometheus.GaugeValue,                                                                           //Type
		float64(d.LastOutSec),                                                                           //Value
		name, uniqueId, d.Name, d.UniqueId, strings.Join(d.LocalTS, ";"), strings.Join(d.RemoteTS, ";"), //Labels
	)

	ch <- prometheus.MustNewConstMetric(
		c.saEstablishSecs,                  //Description
		prometheus.GaugeValue,              //Type
		float64(d.EstablishSec),            //Value
		name, uniqueId, d.Name, d.UniqueId, //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.saRekeySecs,                      //Description
		prometheus.GaugeValue,              //Type
		float64(d.RekeySec),                //Value
		name, uniqueId, d.Name, d.UniqueId, //Labels
	)
	ch <- prometheus.MustNewConstMetric(
		c.saLifetimeSecs,                   //Description
		prometheus.GaugeValue,              //Type
		float64(d.LifetimeSec),             //Value
		name, uniqueId, d.Name, d.UniqueId, //Labels
	)
}
func viciBoolToInt(v string) int {
	if v == "yes" {
		return 1
	} else {
		return 0
	}
}
