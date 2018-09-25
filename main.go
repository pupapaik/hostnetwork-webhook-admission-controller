/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/golang/glog"
	"k8s.io/api/admission/v1beta1"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Runtime binary flags
type options struct {
	PortNumber string
}

var (
	// Options runtime binary flags
	Options options
)

func mutateServices(ar v1beta1.AdmissionReview, o *options) *v1beta1.AdmissionResponse {
	var reviewResponse = &v1beta1.AdmissionResponse{
		Allowed: true,
	}

	podResource := metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	if ar.Request.Resource != podResource {
		glog.Errorf("expect resource to be %s", podResource)
		return nil
	}

	raw := ar.Request.Object.Raw
	pod := v1.Pod{}
	if err := json.Unmarshal(raw, &pod); err != nil {
		glog.Error(err)
		return nil
	}

	addPodHostNetworking := fmt.Sprintln(`[
		{"op":"add","path":"/spec/hostNetwork","value": true},
		{"op":"add","path":"/spec/dnsPolicy","value": "ClusterFirstWithHostNet"},
		{"op":"add","path":"/metadata/annotations","value":{"ves.io/mutating-webhook":"hostnetwork-dnspolicy"}}
   ]`)

	glog.V(2).Infof("patching hostNetworking to pod: %v", pod.ObjectMeta.Name)
	reviewResponse.Patch = []byte(addPodHostNetworking)
	pt := v1beta1.PatchTypeJSONPatch
	reviewResponse.PatchType = &pt

	return reviewResponse
}

type admitFunc func(v1beta1.AdmissionReview, *options) *v1beta1.AdmissionResponse

func serve(w http.ResponseWriter, r *http.Request, o *options, admit admitFunc) {
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		glog.Errorf("contentType=%s, expect application/json", contentType)
		return
	}

	var reviewResponse *v1beta1.AdmissionResponse
	ar := v1beta1.AdmissionReview{}
	if err := json.Unmarshal(body, &ar); err != nil {
		glog.Error(err)
		reviewResponse = &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	} else {
		reviewResponse = admit(ar, o)
	}

	response := v1beta1.AdmissionReview{
		Response: reviewResponse,
	}

	resp, err := json.Marshal(response)
	if err != nil {
		glog.Error(err)
	}
	if _, err := w.Write(resp); err != nil {
		glog.Error(err)
	}
}

func serveMutateServices(w http.ResponseWriter, r *http.Request) {
	serve(w, r, &Options, mutateServices)
}

func serveHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func main() {
	certKey := certKey{}
	flag.StringVar(&Options.PortNumber, "port", "8443", "webserver port")
	flag.StringVar(&certKey.PairName, "keypairname", "tls", "certificate and key pair name")
	flag.StringVar(&certKey.CertDirectory, "certdir", "/var/run/hostnetwork-webhook-admission-controller", "certificate and key directory")
	flag.Parse()

	http.HandleFunc("/mutating-services", serveMutateServices)
	http.HandleFunc("/healthz", serveHealthz)
	clientset := getClient()
	server := &http.Server{
		Addr:      fmt.Sprintf(":%s", Options.PortNumber),
		TLSConfig: configTLS(clientset, &certKey),
	}

	glog.V(2).Infof("starting webserver on port %s", Options.PortNumber)

	if err := server.ListenAndServeTLS("", ""); err != nil {
		glog.Fatal(err)
	}

}
