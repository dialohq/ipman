print "Uninstalling im"
helm list -o json | from json | where name =~ "im" | get name | each {|it| helm uninstall $it}

print "making docker"
make all

print "Waiting"
loop { let exists = (kubectl get ns -o json | from json | get items.metadata.name | where $it =~ "ipman-system" | length); if $exists == 0 { print "done"; break } else { print "..."; sleep 0.5sec } }

print "Installing"
helm install im ./helm --set global.monitoring.enabled=true --set global.monitoring.release=kps 

print "Waiting for controller"
loop {let running = (kubectl get pods -n ipman-system -o json | from json | get items | where $it.metadata.name =~ im-ipman | get status.phase | where $it =~ "Running" | length); if $running == 1 { print "Running"; break;} else {print "..."}}
print "Done"

print "Applying samples"
kubectl apply -f samples/vlan.yaml -f samples/charonsample.yaml # -f samples/charon2sample.yaml  
