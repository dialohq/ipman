package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"dialo.ai/ipman/pkg/netconfig"
	ip "github.com/vishvananda/netlink"
	"k8s.io/klog/v2"
)

func main() {
	flag.Set("v", "5")
	flag.Parse()

	targetPID := os.Getenv("TARGET_PID")
	targetPIDInt, err := strconv.ParseInt(targetPID, 10, 64)
	if err != nil {
		klog.Info("Couldn't parse target PID to int: ", err)
		os.Exit(1)
	}

	klog.Info("Target PID:", targetPID)

	xfrmID := os.Getenv("XFRM_ID")
	xfrmIDInt, err := strconv.ParseInt(xfrmID, 10, 64)
	if err != nil {
		klog.Info("Couldn't parse xfrm ID to int: ", err)
		os.Exit(1)
	}
	klog.Info("XFRM ID:", xfrmID)

	defaultLink, err := netconfig.FindDefaultInterface()
	if err != nil {
		klog.Error("Error finding default interface", "error", err.Error())
		os.Exit(1)
	}
	klog.Info("Found default interface, name: ", (*defaultLink).Attrs().Name)

	xfrmAttrs := ip.LinkAttrs{
		Name:        fmt.Sprintf("xfrm%s", xfrmID),
		NetNsID:     -1,
		TxQLen:      -1,
		ParentIndex: (*defaultLink).Attrs().Index,
	}
	xfrm := ip.Xfrmi{
		LinkAttrs: xfrmAttrs,
		Ifid:      uint32(xfrmIDInt),
	}

	err = ip.LinkAdd(&xfrm)
	if err != nil {
		klog.Info("Couldn't add xfrm interface: ", err)
		os.Exit(1)
	}

	err = ip.LinkSetNsPid(&xfrm, int(targetPIDInt))
	if err != nil {
		klog.Info("Couldn't move xfrm to netns by PID: ", err)
		os.Exit(1)
	}
}
