REGISTRY ?= plan9better
PLATFORM ?= linux/amd64

GOOS ?= linux
GOARCH ?= amd64
PLATFORM ?= linux/amd64


BINARIES := operator restctl vxlandlord xfrminion-init xfrminion-agent
IMG_operator        := ${REGISTRY}/controller:latest
IMG_restctl         := ${REGISTRY}/restctl:latest
IMG_vxlandlord      := ${REGISTRY}/vxlandlord:latest
IMG_xfrminion-init  := ${REGISTRY}/xfrminion-init:latest
IMG_xfrminion-agent := ${REGISTRY}/xfrminion-agent:latest

.PHONY: all build docker push deploy clean all-clean

all: build docker push deploy

all-clean: build docker push clean deploy

build: $(BINARIES:%=build-%)

build-%:
	@echo "Building binary: $*"
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build -o bin/$* ./cmd/$*


docker: $(BINARIES:%=docker-%)

docker-%:
	@echo "Building docker image: $*"
	docker build --platform ${PLATFORM} \
		--build-arg TARGET=$* \
		-t $(IMG_$*) .

push: $(BINARIES:%=push-%)

push-%:
	@echo "Pushing docker image: $*"
	docker push $(IMG_$*)

deploy:
	cd config && kubectl apply                   \
		-f namespace.yaml                         \
		-f service_account.yaml                   \
		-f cluster_role.yaml                      \
		-f cluster_role_binding.yaml              \
		-f webhook_service.yaml                   \
		-f ipman.yaml                             \
		-f ipman-controller-service.yaml          \
		-f mutating_webhook.yaml                  \
		-f network_policy.yaml

	kubectl create secret tls webhook-server-cert \
		--cert certs/server.pem                   \
		--key certs/server-key.pem                \
		-n ims --dry-run=client -o yaml | kubectl apply -f -

	kubectl apply -f samples/ipman.yaml           \
		-f config/controller_deployment.yaml

clean:
	-cd config && kubectl delete                   \
		-f service_account.yaml                    \
		-f cluster_role.yaml                       \
		-f cluster_role_binding.yaml               \
		-f webhook_service.yaml                    \
		-f ipman.yaml                              \
		-f mutating_webhook.yaml                   \
		-f ipman-controller-service.yaml           \
		-f namespace.yaml || true

	-kubectl delete secret webhook-server-cert -n ims || true

nix-build:
	nix run .#vxlandlordImage.copyToRegistry
	nix run .#xfrminion-initImage.copyToRegistry
	nix run .#xfrminion-agentImage.copyToRegistry
	nix run .#operatorImage.copyToRegistry
	nix run .#restctlImage.copyToRegistry

vxlandlord:
	docker build -t plan9better/vxlandlord:latest --platform linux/amd64 --file ./vxlandlord.Dockerfile .
	docker push plan9better/vxlandlord:latest 
xfrminit:
	docker build -t plan9better/xfrminion-init:latest --platform linux/amd64 --file ./xfrminit.Dockerfile .
	docker push plan9better/xfrminion-init:latest 
xfrmagent:
	docker build -t plan9better/xfrminion-agent:latest --platform linux/amd64 --file ./xfrmagent.Dockerfile .
	docker push plan9better/xfrminion-agent:latest 
restctl:
	docker build -t plan9better/restctl:latest --platform linux/amd64 --file ./restctl.Dockerfile .
	docker push plan9better/restctl:latest 
operator:
	docker build -t plan9better/operator:latest --platform linux/amd64 --file ./operator.Dockerfile .
	docker push plan9better/operator:latest 
