package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/pkg/comms"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type stateServer struct {
	client.Client
	rest.Config
}

func writeError(w http.ResponseWriter, err error) {
	w.Header().Add("Content-Type", "application/json")
	rd := &comms.StateResponseData{
		Error: err.Error(),
	}
	w.WriteHeader(400)
	json.NewEncoder(w).Encode(rd)
}

func (s *stateServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rd := &comms.StateRequestData{}
	out, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error(err, "Error reading request body", "request", *r)
		writeError(w, err)
		return
	}

	err = json.Unmarshal(out, rd)
	if err != nil {
		logger.Error(err, "Error unmarshaling request body", "body", string(out))
		writeError(w, err)
		return
	}

	ctx := context.Background()
	nsn := types.NamespacedName{
		Namespace: "ims",
		Name:      rd.IpmanName,
	}

	ipman := &ipmanv1.Ipman{}
	err = s.Client.Get(ctx, nsn, ipman)
	if err != nil {
		logger.Error(err, "Error getting ipman", "requestData", *rd)
		writeError(w, err)
		return
	}

	resp := &comms.StateResponseData{
		Ipman: *ipman,
	}

	respJSON, err := json.Marshal(resp)
	if err != nil {
		logger.Error(err, "Error response data", "responseData", resp)
		writeError(w, err)
		return
	}

	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write(respJSON)
}
