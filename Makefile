REGISTRY ?= plan9better
PLATFORM ?= linux/amd64

GOOS ?= linux
GOARCH ?= amd64
PLATFORM ?= linux/amd64


BINARIES := operator restctl vxlandlord xfrminion
IMG_operator        := ${REGISTRY}/controller:latest
IMG_restctl         := ${REGISTRY}/restctl:latest
IMG_vxlandlord      := ${REGISTRY}/vxlandlord:latest
IMG_xfrminion       := ${REGISTRY}/xfrminion:latest

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
		-f validating_webhook.yaml                \
		-f network_policy.yaml

	kubectl create secret tls webhook-server-cert \
		--cert certs/server.pem                   \
		--key certs/server-key.pem                \
		-n ims --dry-run=client -o yaml | kubectl apply -f -

	kubectl apply -f config/controller_deployment.yaml

clean:
	-cd config && kubectl delete                   \
		-f service_account.yaml                    \
		-f cluster_role.yaml                       \
		-f cluster_role_binding.yaml               \
		-f webhook_service.yaml                    \
		-f ipman.yaml                              \
		-f mutating_webhook.yaml                   \
		-f validating_webhook.yaml                 \
		-f ipman-controller-service.yaml           \
		-f namespace.yaml || true

vxlandlord:
	docker build -t plan9better/vxlandlord:0.1.5 --platform linux/amd64 --file ./vxlandlord.Dockerfile .
	docker push plan9better/vxlandlord:0.1.5 
xfrminion:
	docker build -t plan9better/xfrminion:0.1.5 --platform linux/amd64 --file ./xfrminion.Dockerfile .
	docker push plan9better/xfrminion:0.1.5 
restctl:
	docker build -t plan9better/restctl:0.1.5 --platform linux/amd64 --file ./restctl.Dockerfile .
	docker push plan9better/restctl:0.1.5
operator:
	docker build -t plan9better/operator:0.1.5 --platform linux/amd64 --file ./operator.Dockerfile .
	docker push plan9better/operator:0.1.5 
charon:
	docker build -t plan9better/charon:0.1.5 --platform linux/amd64 --file ./charon.Dockerfile .
	docker push plan9better/charon:0.1.5 
