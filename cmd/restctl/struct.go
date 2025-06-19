package main

type LoadedIKE struct {
	Name         string
	UniqueId     string                 `vici:"uniqueid"`
	Version      int                    `vici:"version"`
	State        string                 `vici:"state"`
	LocalHost    string                 `vici:"local-host"`
	RemoteHost   string                 `vici:"remote-host"`
	Initiator    string                 `vici:"initiator"`
	NatRemote    string                 `vici:"nat-remote"`
	NatFake      string                 `vici:"nat-fake"`
	EncAlg       string                 `vici:"encr-alg"`
	EncKey       int                    `vici:"encr-keysize"`
	IntegAlg     string                 `vici:"integ-alg"`
	IntegKey     int                    `vici:"integ-keysize"`
	DHGroup      string                 `vici:"dh-group"`
	EstablishSec int64                  `vici:"established"`
	RekeySec     int64                  `vici:"rekey-time"`
	ReauthSec    int64                  `vici:"reauth-time"`
	Children     map[string]LoadedChild `vici:"child-sas"`
}
type LoadedChild struct {
	Name         string   `vici:"name"`
	UniqueId     string   `vici:"uniqueid"`
	State        string   `vici:"state"`
	Mode         string   `vici:"mode"`
	Protocol     string   `vici:"protocol"`
	Encap        string   `vici:"encap"`
	EncAlg       string   `vici:"encr-alg"`
	EncKey       int      `vici:"encr-keysize"`
	IntegAlg     string   `vici:"integ-alg"`
	IntegKey     int      `vici:"integ-keysize"`
	DHGroup      string   `vici:"dh-group"`
	BytesIn      int64    `vici:"bytes-in"`
	PacketsIn    int64    `vici:"bytes-out"`
	LastInSec    int64    `vici:"use-in"`
	BytesOut     int64    `vici:"bytes-out"`
	PacketsOut   int64    `vici:"bytes-out"`
	LastOutSec   int64    `vici:"use-out"`
	EstablishSec int64    `vici:"install-time"`
	RekeySec     int64    `vici:"rekey-time"`
	LifetimeSec  int64    `vici:"life-time"`
	LocalTS      []string `vici:"local-ts"`
	RemoteTS     []string `vici:"remote-ts"`
}
