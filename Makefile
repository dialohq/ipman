VERSION ?= 0.1.15
DOCKERHUB_USER ?= plan9better
LOCAL_REGISTRY ?= 192.168.10.201:5000

.PHONY: publish test all vxlandlord xfrminion restctl charon operator

publish:
	docker build -t $(DOCKERHUB_USER)/vxlandlord:$(VERSION) --platform linux/amd64 --file ./vxlandlord.Dockerfile --build-arg PLATFORM=amd64 .
	docker push $(DOCKERHUB_USER)/vxlandlord:$(VERSION) 

	docker build -t $(DOCKERHUB_USER)/xfrminion:$(VERSION) --platform linux/amd64 --file ./xfrminion.Dockerfile --build-arg PLATFORM=amd64 .
	docker push $(DOCKERHUB_USER)/xfrminion:$(VERSION) 

	docker build -t $(DOCKERHUB_USER)/restctl:$(VERSION) --platform linux/amd64 --file ./restctl.Dockerfile --build-arg PLATFORM=amd64 .
	docker push $(DOCKERHUB_USER)/restctl:$(VERSION)

	docker build -t $(DOCKERHUB_USER)/operator:$(VERSION) --platform linux/amd64 --file ./operator.Dockerfile --build-arg PLATFORM=amd64 .
	docker push $(DOCKERHUB_USER)/operator:$(VERSION) 

	docker build -t $(DOCKERHUB_USER)/charon:$(VERSION) --platform linux/amd64 --file ./charon.Dockerfile --build-arg PLATFORM=amd64 .
	docker push $(DOCKERHUB_USER)/charon:$(VERSION) 

	docker build -t $(DOCKERHUB_USER)/xfrminjector:$(VERSION) --platform linux/amd64 --file ./xfrminjector.Dockerfile --build-arg PLATFORM=amd64 .
	docker push $(DOCKERHUB_USER)/xfrminjector:$(VERSION) 

test:
	docker build -t $(DOCKERHUB_USER)/vxlandlord:latest-dev-test --platform linux/arm64 --file ./vxlandlord.Dockerfile --build-arg PLATFORM=arm64 .
	docker push $(DOCKERHUB_USER)/vxlandlord:latest-dev-test 

	docker build -t $(DOCKERHUB_USER)/xfrminion:latest-dev-test --platform linux/arm64 --file ./xfrminion.Dockerfile --build-arg PLATFORM=arm64 .
	docker push $(DOCKERHUB_USER)/xfrminion:latest-dev-test 

	docker build -t $(DOCKERHUB_USER)/restctl:latest-dev-test --platform linux/arm64 --file ./restctl.Dockerfile --build-arg PLATFORM=arm64 .
	docker push $(DOCKERHUB_USER)/restctl:latest-dev-test

	docker build -t $(DOCKERHUB_USER)/operator:latest-dev-test --platform linux/arm64 --file ./operator.Dockerfile --build-arg PLATFORM=arm64 .
	docker push $(DOCKERHUB_USER)/operator:latest-dev-test 

	docker build -t $(DOCKERHUB_USER)/charon:latest-dev-test --platform linux/arm64 --file ./charon.Dockerfile --build-arg PLATFORM=arm64 .
	docker push $(DOCKERHUB_USER)/charon:latest-dev-test 

	docker build -t $(DOCKERHUB_USER)/xfrminjector:latest-dev-test --platform linux/arm64 --file ./xfrminjector.Dockerfile --build-arg PLATFORM=arm64 .
	docker push $(DOCKERHUB_USER)/xfrminjector:latest-dev-test 



vxlandlord:
	docker build -t $(LOCAL_REGISTRY)/vxlandlord:latest-dev --platform linux/arm64 --file ./vxlandlord.Dockerfile .
	docker push $(LOCAL_REGISTRY)/vxlandlord:latest-dev 
xfrminion:
	docker build -t $(LOCAL_REGISTRY)/xfrminion:latest-dev --platform linux/arm64 --file ./xfrminion.Dockerfile .
	docker push $(LOCAL_REGISTRY)/xfrminion:latest-dev 
restctl:
	docker build -t $(LOCAL_REGISTRY)/restctl:latest-dev --platform linux/arm64 --file ./restctl.Dockerfile .
	docker push $(LOCAL_REGISTRY)/restctl:latest-dev
operator:
	docker build -t $(LOCAL_REGISTRY)/operator:latest-dev --platform linux/arm64 --file ./operator.Dockerfile .
	docker push $(LOCAL_REGISTRY)/operator:latest-dev 
charon:
	docker build -t $(LOCAL_REGISTRY)/charon:latest-dev --platform linux/arm64 --file ./charon.Dockerfile .
	docker push $(LOCAL_REGISTRY)/charon:latest-dev 
injector:
	docker build -t $(LOCAL_REGISTRY)/xfrminjector:latest-dev --platform linux/arm64 --file ./xfrminjector.Dockerfile .
	docker push $(LOCAL_REGISTRY)/xfrminjector:latest-dev 

all: vxlandlord xfrminion restctl operator charon injector
