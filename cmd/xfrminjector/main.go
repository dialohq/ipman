package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/pkg/netconfig"

	ip "github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
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

	sourcePIDBytes, err := os.ReadFile(ipmanv1.CharonSocketVolumeMountPath + "charon.pid")
	if err != nil {
		klog.Error("Couldn't open mounted file 'charon.pid': ", err)
		os.Exit(1)
	}
	sourcePIDInt, err := strconv.ParseInt(strings.TrimSpace(string(sourcePIDBytes)), 10, 64)
	if err != nil {
		klog.Errorf("Couldn't parse netns pid '%s' to int: %s", string(sourcePIDBytes), err.Error())
		os.Exit(1)
	}

	ns, err := netns.GetFromPid(int(sourcePIDInt))
	if err != nil {
		klog.Errorf("Couldn't get netns by PID '%d': %s", sourcePIDInt, err.Error())
		os.Exit(1)
	}

	// linux ns's are per thread, make sure
	// the executing thread doesn't change
	runtime.LockOSThread()
	defer func() {
		klog.Info("Unlocking thread")
		runtime.UnlockOSThread()
	}()

	originalNs, err := netns.Get()
	if err != nil {
		klog.Errorf("Couldn't get handle to the original netns: %s", err.Error())
		os.Exit(1)
	}
	defer func() {
		klog.Info("Going back to original netns")
		netns.Set(originalNs)
	}()

	err = netns.Set(ns)
	if err != nil {
		klog.Error("Couldn't set netns from PID %d: %s", sourcePIDInt, err.Error())
		os.Exit(1)
	}
	var finalLink *ip.Link
	ifName := os.Getenv("INTERFACE_NAME")
	if ifName == "" {
		finalLink, err = netconfig.FindDefaultInterface()
		if err != nil {
			klog.Errorf("Couldn't find default interface: %s", err.Error())
			os.Exit(1)
		}
	} else {
		d, err := ip.LinkByName(ifName)
		if err != nil {
			klog.Error("Error finding interface ", ifName, " error: ", err.Error())
			os.Exit(1)
		}
		finalLink = &d
	}

	klog.Infof("Interface name set to %s", (*finalLink).Attrs().Name)

	xfrmAttrs := ip.LinkAttrs{
		Name:        fmt.Sprintf("xfrm%s", xfrmID),
		NetNsID:     -1,
		TxQLen:      -1,
		ParentIndex: (*finalLink).Attrs().Index,
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
