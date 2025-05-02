IMG ?= plan9better/controller:latest

.PHONY: build
build:
	docker build -t ${IMG}  --platform linux/amd64 .

.PHONY: build-dev
build-dev:
	docker build -t ${IMG} --platform linux/arm64 .

.PHONY: push
push:
	docker push ${IMG}

.PHONY: deploy
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
		-n ims

	kubectl apply -f samples/ipman.yaml           \
		-f config/controller_deployment.yaml

.PHONY: clean
clean:
	-cd config && kubectl delete                   \
		-f service_account.yaml                   \
		-f cluster_role.yaml                      \
		-f cluster_role_binding.yaml              \
		-f webhook_service.yaml                   \
		-f ipman.yaml                             \
		-f mutating_webhook.yaml                  \
		-f ipman-controller-service.yaml          \
		-f namespace.yaml                         

	-kubectl delete secret webhook-server-cert

.PHONY: all
all: build push deploy	

.PHONY: all-clean
all-clean: build push clean deploy
