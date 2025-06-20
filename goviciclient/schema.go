package goviciclient

// Observable is a type that can be observed.
type LocalAuth struct {
	Auth    string   `vici:"auth" json:"auth"`
	Class   string   `vici:"class" json:"class"`
	ID      string   `vici:"id" json:"id"`
	Groups  []string `vici:"groups" json:"groups"`
	Certs   []string `vici:"certs" json:"certs"`
	Cacerts []string `vici:"cacerts" json:"cacerts"`
}

type RemoteAuth struct {
	Auth    string   `vici:"auth" json:"auth"`
	Class   string   `vici:"class" json:"class"`
	ID      string   `vici:"id" json:"id"`
	Groups  []string `vici:"groups" json:"groups"`
	Certs   []string `vici:"certs" json:"certs"`
	Cacerts []string `vici:"cacerts" json:"cacerts"`
}

type ChildSA struct {
	Mode     string   `vici:"mode" json:"mode"`
	LocalTS  []string `vici:"local-ts" json:"local_ts"`
	RemoteTS []string `vici:"remote-ts" json:"remote_ts"`
}

type IKEConnection struct {
	LocalAddrs  []string           `vici:"local_addrs" json:"local_addrs"`
	RemoteAddrs []string           `vici:"remote_addrs" json:"remote_addrs"`
	Version     string             `vici:"version" json:"version"`
	ReauthTime  int                `vici:"reauth_time" json:"reauth_time"`
	RekeyTime   int                `vici:"rekey_time" json:"rekey_time"`
	LocalAuths  LocalAuth          `vici:"local" json:"local"`
	RemoteAuths RemoteAuth         `vici:"remote" json:"remote"`
	Extra       map[string]string  `json:"extra"`
	Children    map[string]ChildSA `vici:"children" json:"children"`
}

// ==============config strict================
type ChildSAConfig struct {
	Mode           string            `vici:"mode" json:"mode"`
	StartAction    string            `vici:"start_action,omitempty" json:"start_action,omitempty"`
	EspProposals   []string          `vici:"esp_proposals,omitempty" json:"esp_proposals,omitempty"`
	LocalTS        []string          `vici:"local_ts" json:"local_ts"`
	RemoteTS       []string          `vici:"remote_ts" json:"remote_ts"`
	RekeyTime      string            `vici:"rekey_time,omitempty" json:"rekey_time,omitempty"`
	RandPackets    string            `vici:"rand_packets,omitempty" json:"rand_packets,omitempty"`
	Updown         string            `vici:"updown,omitempty" json:"updown,omitempty"`
	Hostaccess     string            `vici:"hostaccess,omitempty" json:"hostaccess,omitempty"`
	Policies       string            `vici:"policies,omitempty" json:"policies,omitempty"`
	SetMarkIn      string            `vici:"set_mark_in,omitempty" json:"set_mark_in,omitempty"`
	SetMarkOut     string            `vici:"set_mark_out,omitempty" json:"set_mark_out,omitempty"`
	DpdAction      string            `vici:"dpd_action,omitempty" json:"dpd_action,omitempty"`
	CloseAction    string            `vici:"close_action,omitempty" json:"close_action,omitempty"`
	InInterfaceID  int               `vici:"if_id_in" json:"if_id_in"`
	OutInterfaceID int               `vici:"if_id_out" json:"if_id_out"`
	Extra          map[string]string `json:"extra,omitempty"`
}

// IKE|EAP|XAUTH|NTLM
type SharedSecretType int

const (
	SecretIKE SharedSecretType = iota
	SecretEAP
	SecretXAUTH
	SecretNTLM
)

var SharedSecretValue = map[SharedSecretType]string{
	SecretIKE:   "IKE",
	SecretEAP:   "EAP",
	SecretXAUTH: "XAUTH",
	SecretNTLM:  "NTLM",
}

func (sst SharedSecretType) String() string {
	return SharedSecretValue[sst]
}

type LocalAuthConfig struct {
	ID      string   `vici:"id" json:"id"`
	Auth    string   `vici:"auth" json:"auth"`
	Certs   []string `vici:"certs" json:"certs"`
	Cacerts []string `vici:"cacerts" json:"cacerts"`
}

type RemoteAuthConfig struct {
	ID      string   `vici:"id" json:"id"`
	Auth    string   `vici:"auth" json:"auth"`
	Certs   []string `vici:"certs" json:"certs"`
	Cacerts []string `vici:"cacerts" json:"cacerts"`
}

type IKEConfig struct {
	LocalAddrs     []string                 `vici:"local_addrs" json:"local_addrs"`
	RemoteAddrs    []string                 `vici:"remote_addrs" json:"remote_addrs"`
	LocalPort      string                   `vici:"local_port,omitempty" json:"local_port,omitempty"`
	RemotePort     string                   `vici:"remote_port,omitempty" json:"remote_port,omitempty"`
	Proposals      []string                 `vici:"proposals" json:"proposals"`
	Version        string                   `vici:"version" json:"version"`
	Pull           string                   `vici:"pull,omitempty" json:"pull,omitempty"`
	Dscp           string                   `vici:"dscp,omitempty" json:"dscp,omitempty"`
	Encap          string                   `vici:"encap,omitempty" json:"encap,omitempty"`
	Mobike         string                   `vici:"mobike,omitempty" json:"mobike,omitempty"`
	DpdDelay       string                   `vici:"dpd_delay,omitempty" json:"dpd_delay,omitempty"`
	DpdTimeout     string                   `vici:"dpd_timeout,omitempty" json:"dpd_timeout,omitempty"`
	Fragmentation  string                   `vici:"fragmentation,omitempty" json:"fragmentation,omitempty"`
	Childless      string                   `vici:"childless,omitempty" json:"childless,omitempty"`
	SendCertreq    string                   `vici:"send_certreq,omitempty" json:"send_certreq,omitempty"`
	SendCert       string                   `vici:"send_cert,omitempty" json:"send_cert,omitempty"`
	PpkId          string                   `vici:"ppk_id,omitempty" json:"ppk_id,omitempty"`
	PpkRequired    string                   `vici:"ppk_required,omitempty" json:"ppk_required,omitempty"`
	Keyingtries    string                   `vici:"keyingtries,omitempty" json:"keyingtries,omitempty"`
	Unique         string                   `vici:"unique,omitempty" json:"unique,omitempty"`
	ReauthTime     int                      `vici:"reauth_time" json:"reauth_time"`
	RekeyTime      int                      `vici:"rekey_time" json:"rekey_time"`
	OverTime       string                   `vici:"over_time,omitempty" json:"over_time,omitempty"`
	RandTime       string                   `vici:"rand_time,omitempty" json:"rand_time,omitempty"`
	Pools          []string                 `vici:"pools,omitempty" json:"pools,omitempty"`
	IfIdIn         string                   `vici:"if_id_in,omitempty" json:"if_id_in,omitempty"`
	IfIdOut        string                   `vici:"if_id_out,omitempty" json:"if_id_out,omitempty"`
	Mediation      string                   `vici:"mediation,omitempty" json:"mediation,omitempty"`
	MediatedBy     string                   `vici:"mediated_by,omitempty" json:"mediated_by,omitempty"`
	MediationPeer  string                   `vici:"mediation_peer,omitempty" json:"mediation_peer,omitempty"`
	CloseAction    string                   `vici:"close_action,omitempty" json:"close_action,omitempty"`
	LocalAuths     *LocalAuthConfig         `vici:"local" json:"local"`
	RemoteAuths    *RemoteAuthConfig        `vici:"remote" json:"remote"`
	Children       map[string]ChildSAConfig `vici:"children" json:"children"`
}

type ConnectionsMap map[string]IKEConnection
type ConnectionsNames struct {
	Conns []string `vici:"conns" json:"conns"`
}

type IkeSaStatus map[string]IkeSa

type IkeSa struct {
	UniqueID      string             `json:"uniqueid" vici:"uniqueid"`
	Version       string             `json:"version" vici:"version"`
	State         string             `json:"state" vici:"state"`
	LocalHost     string             `json:"local-host" vici:"local-host"`
	LocalPort     string             `json:"local-port" vici:"local-port"`
	LocalID       string             `json:"local-id" vici:"local-id"`
	RemoteHost    string             `json:"remote-host" vici:"remote-host"`
	RemotePort    string             `json:"remote-port" vici:"remote-port"`
	RemoteID      string             `json:"remote-id" vici:"remote-id"`
	RemoteXauthID string             `json:"remote-xauth-id,omitempty" vici:"remote-xauth-id"`
	RemoteEapID   string             `json:"remote-eap-id,omitempty" vici:"remote-eap-id"`
	Initiator     string             `json:"initiator" vici:"initiator"`
	InitiatorSPI  string             `json:"initiator-spi" vici:"initiator-spi"`
	ResponderSPI  string             `json:"responder-spi" vici:"responder-spi"`
	NatLocal      string             `json:"nat-local,omitempty" vici:"nat-local"`
	NatRemote     string             `json:"nat-remote,omitempty" vici:"nat-remote"`
	NatFake       string             `json:"nat-fake,omitempty" vici:"nat-fake"`
	NatAny        string             `json:"nat-any,omitempty" vici:"nat-any"`
	IfIDIn        string             `json:"if-id-in,omitempty" vici:"if-id-in"`
	IfIDOut       string             `json:"if-id-out,omitempty" vici:"if-id-out"`
	EncrAlg       string             `json:"encr-alg" vici:"encr-alg"`
	EncrKeySize   string             `json:"encr-keysize,omitempty" vici:"encr-keysize"`
	IntegAlg      string             `json:"integ-alg,omitempty" vici:"integ-alg"`
	IntegKeySize  string             `json:"integ-keysize,omitempty" vici:"integ-keysize"`
	PrfAlg        string             `json:"prf-alg,omitempty" vici:"prf-alg"`
	DhGroup       string             `json:"dh-group,omitempty" vici:"dh-group"`
	Established   string             `json:"established,omitempty" vici:"established"`
	RekeyTime     string             `json:"rekey-time,omitempty" vici:"rekey-time"`
	ReauthTime    string             `json:"reauth-time,omitempty" vici:"reauth-time"`
	LocalVips     []string           `json:"local-vips,omitempty" vici:"local-vips"`
	RemoteVips    []string           `json:"remote-vips,omitempty" vici:"remote-vips"`
	TasksQueued   []string           `json:"tasks-queued,omitempty" vici:"tasks-queued"`
	TasksActive   []string           `json:"tasks-active,omitempty" vici:"tasks-active"`
	TasksPassive  []string           `json:"tasks-passive,omitempty" vici:"tasks-passive"`
	ChildSAs      map[string]ChildSa `json:"child-sas,omitempty" vici:"child-sas"`
}

type ChildSa struct {
	Name         string   `json:"name" vici:"name"`
	UniqueID     string   `json:"uniqueid" vici:"uniqueid"`
	ReqID        string   `json:"reqid" vici:"reqid"`
	State        string   `json:"state" vici:"state"`
	Mode         string   `json:"mode" vici:"mode"`
	Protocol     string   `json:"protocol" vici:"protocol"`
	Encap        string   `json:"encap,omitempty" vici:"encap"`
	SPIIn        string   `json:"spi-in" vici:"spi-in"`
	SPIOut       string   `json:"spi-out" vici:"spi-out"`
	CPIIn        string   `json:"cpi-in,omitempty" vici:"cpi-in"`
	CPIOut       string   `json:"cpi-out,omitempty" vici:"cpi-out"`
	MarkIn       string   `json:"mark-in,omitempty" vici:"mark-in"`
	MarkMaskIn   string   `json:"mark-mask-in,omitempty" vici:"mark-mask-in"`
	MarkOut      string   `json:"mark-out,omitempty" vici:"mark-out"`
	MarkMaskOut  string   `json:"mark-mask-out,omitempty" vici:"mark-mask-out"`
	IfIDIn       string   `json:"if-id-in,omitempty" vici:"if-id-in"`
	IfIDOut      string   `json:"if-id-out,omitempty" vici:"if-id-out"`
	PerCpuSAs    string   `json:"per-cpu-sas,omitempty" vici:"per-cpu-sas"`
	CPU          string   `json:"cpu,omitempty" vici:"cpu"`
	Label        string   `json:"label,omitempty" vici:"label"`
	EncrAlg      string   `json:"encr-alg,omitempty" vici:"encr-alg"`
	EncrKeySize  string   `json:"encr-keysize,omitempty" vici:"encr-keysize"`
	IntegAlg     string   `json:"integ-alg,omitempty" vici:"integ-alg"`
	IntegKeySize string   `json:"integ-keysize,omitempty" vici:"integ-keysize"`
	PrfAlg       string   `json:"prf-alg,omitempty" vici:"prf-alg"`
	DhGroup      string   `json:"dh-group,omitempty" vici:"dh-group"`
	ESN          string   `json:"esn,omitempty" vici:"esn"`
	BytesIn      string   `json:"bytes-in" vici:"bytes-in"`
	PacketsIn    string   `json:"packets-in" vici:"packets-in"`
	UseIn        string   `json:"use-in,omitempty" vici:"use-in"`
	BytesOut     string   `json:"bytes-out" vici:"bytes-out"`
	PacketsOut   string   `json:"packets-out" vici:"packets-out"`
	UseOut       string   `json:"use-out,omitempty" vici:"use-out"`
	RekeyTime    string   `json:"rekey-time,omitempty" vici:"rekey-time"`
	LifeTime     string   `json:"life-time,omitempty" vici:"life-time"`
	InstallTime  string   `json:"install-time,omitempty" vici:"install-time"`
	LocalTS      []string `json:"local-ts,omitempty" vici:"local-ts"`
	RemoteTS     []string `json:"remote-ts,omitempty" vici:"remote-ts"`
}
