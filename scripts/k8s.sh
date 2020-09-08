#!/bin/sh
# Point to the internal API server hostname
K8S_APISERVER=https://kubernetes.default.svc

# Path to ServiceAccount token
K8S_SERVICEACCOUNT=/var/run/secrets/kubernetes.io/serviceaccount

# Read this Pod's namespace
K8S_NAMESPACE=$(cat ${K8S_SERVICEACCOUNT}/namespace)

# Read the ServiceAccount bearer token
K8S_TOKEN=$(cat ${K8S_SERVICEACCOUNT}/token)

# Reference the internal certificate authority (CA)
K8S_CACERT=${K8S_SERVICEACCOUNT}/ca.crt

k8s_get() {
    status=$(curl -s -w '%{http_code}\n' -o /tmp/res.json --cacert ${K8S_CACERT} --header "Authorization: Bearer ${K8S_TOKEN}" -X GET "${K8S_APISERVER}/$1")
    if [ "$(echo $status | cut -c1)" -ne 2 ]; then
        echo "GET $1: HTTP $status error!" 1>&2
    fi

    cat /tmp/res.json
}
