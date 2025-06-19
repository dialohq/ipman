package goviciclient

import (
	"testing"
)

func TestExampleUsage(t *testing.T) {
	// 创建一个新的 VICI 客户端
	opts := &clientOpts{
		Network: "tcp",
		Addr:    "192.168.23.199:1199",
	}
	client, err := NewViciClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	// cs, err := client.ListConns("")
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// t.Log(cs)

	// // 获取所有连接的名称
	// names, err := client.ListConnsNames()
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// t.Log(names)

	// t.Log(client.ListCerts())

	// 加载连接
	var conns = make(map[string]IKEConfig)
	conns["test"] = IKEConfig{
		LocalAddrs:  []string{"192.168.22.1"},
		RemoteAddrs: []string{"192.168.22.4"},
		Version:     "0",
		ReauthTime:  3600,
		RekeyTime:   3600,
		Proposals:   []string{"aes128-sha256-modp2048"},
		LocalAuths: &LocalAuthConfig{
			Auth: "pubkey",
		},
		RemoteAuths: &RemoteAuthConfig{},
		Children: map[string]ChildSAConfig{
			"test": {
				Mode:         "tunnel",
				StartAction:  "start",
				LocalTS:      []string{"10.10.1.0/24"},
				RemoteTS:     []string{"10.11.1.0/24"},
				EspProposals: []string{"sm4-sm3"},
			},
		},
	}
	err = client.LoadConns(conns)
	if err != nil {
		t.Fatal(err)
	}

	// 删除连接
	// err = client.UnloadConns("test")
	// if err != nil {
	// 	t.Fatal(err)
	// }
}
