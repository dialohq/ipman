package goviciclient

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/strongswan/govici/vici"
)

// ViciClient 包装了与 VICI 协议通信的客户端
type ViciClient struct {
	session *vici.Session
}

type clientOpts struct {
	Addr    string
	Network string
}

// NewViciClient 创建一个新的 VICI 客户端
func NewViciClient(opts *clientOpts) (*ViciClient, error) {
	network := "unix"
	addr := "/var/run/charon.vici"
	if opts != nil {
		if opts.Network != "" {
			network = opts.Network
		}
		if opts.Addr != "" {
			addr = opts.Addr
		}

	}
	session, err := vici.NewSession(vici.WithAddr(network, addr))
	if err != nil {
		return nil, err
	}
	return &ViciClient{session: session}, nil
}

func setKey(msg *vici.Message, key string, value any) error {
	err := msg.Set(key, value)
	if err != nil {
		return fmt.Errorf("Couldn't set key '%s' to '%s': %w", key, value, err)
	}
	return nil
}

func (c *ViciClient) InitChild(timeout int, child, conn string) error {
	msg := vici.NewMessage()
	err := setKey(msg, "timeout", timeout)
	if err != nil {
		return err
	}
	err = setKey(msg, "child", child)
	if err != nil {
		return err
	}
	err = setKey(msg, "ike", conn)
	if err != nil {
		return err
	}
	resp, err := c.streamedCommandRequest("initiate", "log", msg)
	if err != nil {
		return fmt.Errorf("Couldn't request initiating child: %w", err)
	}
	for _, msg := range resp {
		if slices.Contains(msg.Keys(), "success") {
			if msg.Get("success") == "yes" {
				return nil
			}
			return msg.Err()
		}
	}

	return nil
}

func (c *ViciClient) GetShared() ([]string, error) {
	resp, err := c.commandRequest("get-shared", nil)
	if err != nil {
		return nil, fmt.Errorf("Error getting shared secrets: %w", err)
	}
	val, ok := resp.Get("keys").([]string)
	if !ok {
		return nil, fmt.Errorf("Vici returned unexpected type, expected []string, received value: %+v", resp.Get("keys"))
	}
	return val, nil
}

// Options that will be set on the vici message list-sas
type ListSasOpts struct {
	ike     *string `vici:"ike"`
	ikeId   *int    `vici:"ike-id"`
	child   *string `vici:"child"`
	childId *int    `vici:"child-id"`
}

func (c *ViciClient) ListSas(opts *ListSasOpts) (map[string]IkeSa, error) {
	msg := vici.NewMessage()
	if opts != nil {
		val := reflect.ValueOf(opts).Elem()
		typ := val.Type()
		for i := range typ.NumField() {
			field := typ.Field(i)
			tag, ok := field.Tag.Lookup("vici")
			if !ok {
				return nil, fmt.Errorf("Malformed options struct")
			}
			if val.Field(i).Interface() != nil {
				msg.Set(tag, val.Field(i).Interface())
			}
		}
	}

	resp, err := c.streamedCommandRequest("list-sas", "list-sa", msg)
	if err != nil {
		return nil, fmt.Errorf("Couldn't send command request: %w", err)
	}

	sas := map[string]IkeSa{}
	for _, msg := range resp {
		for _, k := range msg.Keys() {
			cfg := msg.Get(k)
			val, ok := cfg.(*vici.Message)
			sa := IkeSa{}
			if !ok {
				return nil, fmt.Errorf("Value is not vici.Message pointer: %+v", cfg)
			} else {
				err := vici.UnmarshalMessage(val, &sa)
				if err != nil {
					return nil, fmt.Errorf("Couldn't unmarshal message into go struct: %+v", val)
				}

				sas[k] = sa
			}
		}
	}

	return sas, nil
}

type Key struct {
	Typ    string   `json:"type" vici:"type"`
	Data   string   `json:"data" vici:"data"`
	Owners []string `json:"owners" vici:"owners"`
}

func (c *ViciClient) LoadShared(k Key) error {
	msg, err := c.marshal(k)
	if err != nil {
		return fmt.Errorf("Couldn't marshal key: %w", err)
	}

	resp, err := c.commandRequest("load-shared", msg)
	if err != nil {
		return fmt.Errorf("Error sending command request: %w", err)
	}
	if resp.Get("success") != "yes" {
		return fmt.Errorf("Couldn't load shared secrets: vici returned success = false")
	}
	return nil
}

func (c *ViciClient) ListConns(ike *string) ([]ConnectionsMap, error) {
	msg := vici.NewMessage()
	if ike != nil {
		msg.Set("ike", ike)
	} else {
		msg.Unset("ike")
	}
	resp, err := c.streamedCommandRequest("list-conns", "list-conn", msg)
	if err != nil {
		return nil, err
	}
	conns := make([]ConnectionsMap, 0)
	for _, m := range resp {
		var conn ConnectionsMap = make(map[string]IKEConnection)
		if err := vici.UnmarshalMessage(m, conn); err != nil {
			return nil, err
		}
		if len(conn) != 0 {
			conns = append(conns, conn)
		}
	}
	return conns, nil
}

func (c *ViciClient) marshal(val any) (*vici.Message, error) {
	return vici.MarshalMessage(val)
}

func (c *ViciClient) ListConnsNames() (*ConnectionsNames, error) {
	resp, err := c.commandRequest("get-conns", vici.NewMessage())
	if err != nil {
		return nil, err
	}
	var names *ConnectionsNames = new(ConnectionsNames)
	if err := vici.UnmarshalMessage(resp, names); err != nil {
		return nil, err
	}
	return names, nil
}

func (c *ViciClient) LoadConns(conns map[string]IKEConfig) error {
	msg := vici.NewMessage()

	for name, conn := range conns {
		connMsg, err := vici.MarshalMessage(conn)
		if err != nil {
			return fmt.Errorf("failed to marshal connection %s: %w", name, err)
		}
		
		err = msg.Set(name, connMsg)
		if err != nil {
			return fmt.Errorf("failed to set connection %s in message: %w", name, err)
		}
	}

	resp, err := c.commandRequest("load-conn", msg)
	if err != nil {
		return fmt.Errorf("load-conn command failed: %w", err)
	}
	
	if resp.Get("success") != "yes" {
		return fmt.Errorf("failed to load connections: vici returned success = false")
	}
	
	return nil
}

func (c *ViciClient) UnloadConns(conns string) error {
	msg := vici.NewMessage()
	msg.Set("name", conns)
	_, err := c.commandRequest("unload-conn", msg)
	return err
}

// Close 关闭 VICI 客户端的会话
func (c *ViciClient) Close() error {
	return c.session.Close()
}

// CommandRequest 发送一个命令请求到服务器，并返回响应
func (c *ViciClient) commandRequest(cmd string, msg *vici.Message) (*vici.Message, error) {
	return c.session.CommandRequest(cmd, msg)
}

// StreamedCommandRequest 发送一个流式命令请求
func (c *ViciClient) streamedCommandRequest(cmd, event string, msg *vici.Message) ([]*vici.Message, error) {
	dupa, err := c.session.StreamedCommandRequest(cmd, event, msg)
	if err != nil {
		return nil, err
	}
	return dupa.Messages(), nil
}

// Subscribe 订阅事件
func (c *ViciClient) subscribe(events ...string) error {
	return c.session.Subscribe(events...)
}

// Unsubscribe 取消订阅事件
func (c *ViciClient) unsubscribe(events ...string) error {
	return c.session.Unsubscribe(events...)
}
