#!/usr/bin/env bash
while true; do
  kubectl logs somepodd2 --container iface-request -f
done;
