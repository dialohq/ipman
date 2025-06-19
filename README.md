[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/ipman)](https://artifacthub.io/packages/search?repo=ipman)
# IPMan - IPSec Connection Manager for Kubernetes
![logo-512x512](https://github.com/user-attachments/assets/8739b49c-f77d-4c76-8091-73f427442bfb)

IPMan is a Kubernetes operator that simplifies the management of IPSec connections, enabling secure communication between your Kubernetes workloads and the outside world. It automates the setup and configuration of IPSec VPN tunnels using StrongSwan, making it easy to expose pods to external networks securely.

## What IPMan Does

- Creates and manages IPSec VPN connections between Kubernetes nodes and remote endpoints
- Handles routing configuration automatically
- Provides IP pool management for your workloads
- Enables secure communication through VPN tunnels

## Installation

### Prerequisites

- Kubernetes cluster 1.20+
- Helm 3.0+
- Linux kernel XFRM module
- A CNI plugin that supports inter-node pod-to-pod communication (e.g., Calico, Cilium)

### Installing with Helm

```bash
# Add the repository
helm repo add ipman https://dialohq.github.io/ipman

# Install the chart
helm install ipman ipman/ipman -n ipman-system --create-namespace
```

## Usage

### Step 1: Create a Secret with Pre-shared Key' 

IPMan requires a secret for IPSec authentication:

```bash
kubectl create secret generic ipsec-secret -n default --from-literal=example=yourpresharedkey
```

### Step 2: Create an IPSecConnection

Create an IPSecConnection Custom Resource (CR) to establish a VPN connection:

```yaml
apiVersion: ipman.dialo.ai/v1
kind: IPSecConnection
metadata:
  name: example-connection
  namespace: ipman-system
spec:
  name: "example"
  remoteAddr: "192.168.1.2"
  localAddr: "192.168.1.1"
  localId: "192.168.1.1"
  remoteId: "192.168.1.2"
  secretRef:
    name: "ipsec-secret"
    namespace: default
    key: "example"
  children:
    example-child:
      name: "example-child"
      extra:
        esp_proposals: aes256-sha256-ecp256
        start_action: start
        dpd_action: restart
      local_ips:
        - "10.0.2.0/24"
      remote_ips:
        - "10.0.1.0/24"
      xfrm_ip: "10.0.2.1/24"
      vxlan_ip: "10.0.2.2/24"
      if_id: 102
      ip_pools:
        primary:
          - "10.0.2.3/24"
          - "10.0.2.4/24"
  nodeName: "your-node-name"
```

### Step 3: Deploy Workloads Using the VPN Connection

To route workload traffic through the VPN tunnel, add specific annotations to your Pods or Deployments. These annotations tell IPMan to allocate IPs from the configured pools and set up the necessary routing.

#### Required Annotations for Worker Pods

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: example-app
spec:
  template:
    metadata:
      annotations:
        ipman.dialo.ai/childName: "example-child"            # Must match the child name in IPSecConnection
        ipman.dialo.ai/ipmanName: "example-connection"       # Must match the IPSecConnection name
        ipman.dialo.ai/poolName: "primary"                   # IP pool to use (defined in IPSecConnection)
    spec:
      # Your pod spec here
```

The operator will automatically:
1. Allocate IPs from the specified pool
2. Set up routing for your workloads
3. Configure bridge FDB entries for communication

## Configuration Reference

### IPSecConnection CR Fields

| Field | Description |
|-------|-------------|
| `name` | Name for the IPSec connection |
| `remoteAddr` | Remote VPN endpoint address |
| `localAddr` | Local VPN endpoint address |
| `localId` | Local identification |
| `remoteId` | Remote identification |
| `secretRef` | Reference to Kubernetes secret containing pre-shared key |
| `children` | Map of child connections (for multiple tunnels) |
| `nodeName` | Kubernetes node to establish connection from |

### Child Connection Fields

| Field | Description |
|-------|-------------|
| `name` | Name for the child connection |
| `local_ips` | List of local networks/IPs for the tunnel |
| `remote_ips` | List of remote networks/IPs for the tunnel |
| `xfrm_ip` | IP for the xfrm interface |
| `vxlan_ip` | IP for the vxlan interface |
| `if_id` | Interface ID |
| `ip_pools` | Named IP pools available for allocation |
| `extra` | Additional StrongSwan configuration options |

## Troubleshooting

If you encounter issues with your IPSec connections:

1. Check the IPSecConnection status:
   ```bash
   kubectl get ipsecconnection -n ipman-system
   kubectl describe ipsecconnection example-connection -n ipman-system
   ```

2. Check the operator logs:
   ```bash
   kubectl logs -n ipman-system -l app=ipman-controller
   ```

3. Verify pod annotations match the IPSecConnection configuration

## Monitoring

IPMan now supports monitoring via Prometheus. To enable monitoring:

1. Set `global.monitoring.enabled` to `true` in your Helm values
2. Set `global.monitoring.release` to the name you've given your Prometheus operator 
   (e.g., if installed via `helm install kps prometheus-community/kube-prometheus-stack`, 
   set it to "kps")

See `helm/values.yaml` for more configuration options.

## Architecture
![image](https://github.com/user-attachments/assets/62ac06dd-8319-432c-9512-c3eebcb54b4d)

### Description
IPMan ensures secure connectivity between remote sites and workload pods by injecting secondary interfaces tied to the local encryption domain's network. Inbound traffic arrives at the host’s network interface, is forwarded through a dedicated XFRM interface, and routed within an isolated VXLAN segment for enhanced security and segmentation.
Charon, the IKE daemon from StrongSwan, operates on user-specified nodes, with instance counts driven by the IPsec configuration. Each child connection runs in a dedicated pod, equipped with its own XFRM interface and VXLAN segment. This design enables flexible workload deployment across the cluster, abstracting the underlying physical infrastructure for seamless scalability. Only ports 500 (IKE) and 4500 (NAT-traversal/IPsec) are exposed for secure communication. Charon and the restctl service, which manage the Charon socket and XFRM interface configuration, operate within the host network namespace, exposing only sockets mounted in the host filesystem. Control sockets, accessible via proxies, facilitate cluster-wide management without requiring additional open ports.

## Acknowledgments
Special thanks to [LarsTi](https://github.com/LarsTi/ipsec_exporter/) for his ipsec_exporter repository, which we've adapted for our use case.

## License

This project is licensed under the [MIT License](./LICENSE).
