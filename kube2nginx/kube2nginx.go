/*
Copyright 2014 The Kubernetes Authors All rights reserved.

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

// kube2nginx is a bridge between Kubernetes and Confd for Nginx.  It watches the
// Kubernetes master for changes in Services annotations and manifests them into etcd for
// Confd to update Nginx configuration.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
	"strconv"
	"path"

	etcd "github.com/coreos/go-etcd/etcd"
	"github.com/golang/glog"
	kapi "k8s.io/kubernetes/pkg/api"
	kclient "k8s.io/kubernetes/pkg/client"
	kcache "k8s.io/kubernetes/pkg/client/cache"
	kclientcmd "k8s.io/kubernetes/pkg/client/clientcmd"
	kframework "k8s.io/kubernetes/pkg/controller/framework"
	kSelector "k8s.io/kubernetes/pkg/fields"
	etcdstorage "k8s.io/kubernetes/pkg/storage/etcd"
	"k8s.io/kubernetes/pkg/util"
	"k8s.io/kubernetes/pkg/util/wait"
)

var (
	// TODO: switch to pflag and make - and _ equivalent.
	argEtcdMutationTimeout = flag.Duration("etcd_mutation_timeout", 10*time.Second, "crash after retrying etcd mutation for a specified duration")
	argEtcdServer          = flag.String("etcd-server", "http://127.0.0.1:4001", "URL to etcd server")
	argKubecfgFile         = flag.String("kubecfg_file", "", "Location of kubecfg file for access to kubernetes master service; --kube_master_url overrides the URL part of this; if neither this nor --kube_master_url are provided, defaults to service account tokens")
	argKubeMasterURL       = flag.String("kube_master_url", "", "URL to reach kubernetes master. Env variables in this flag will be expanded.")
)

const (
	// Maximum number of attempts to connect to etcd server.
	maxConnectAttempts = 12
	// Resync period for the kube controller loop.
	resyncPeriod = 30 * time.Minute
	proxyKey = "/proxy"
)

type etcdClient interface {
	Set(path, value string, ttl uint64) (*etcd.Response, error)
	Get(key string, sort, recursive bool) (*etcd.Response, error)
	RawGet(key string, sort, recursive bool) (*etcd.RawResponse, error)
	Delete(path string, recursive bool) (*etcd.Response, error)
}

type nameNamespace struct {
	name      string
	namespace string
}

type kube2nginx struct {
	// Etcd client.
	etcdClient etcdClient
	// Etcd mutation timeout.
	etcdMutationTimeout time.Duration
	// A cache that contains all the endpoints in the system.
	endpointsStore kcache.Store
	// A cache that contains all the services in the system.
	servicesStore kcache.Store
	// Lock for controlling access to headless services.
	mlock sync.Mutex
}


type proxyService struct {
	Service string
	RawDomain string `json:"domain"`
	RawPath string `json:"path"`
	HTTP bool
	HTTPS bool
	RawTargetPort string
}

func (pService *proxyService) Domain() string {
	if pService.RawDomain != "" {
		return pService.RawDomain
	}
	return "default"
}

func (pService *proxyService) Path() string {
	if pService.RawPath != "" {
		return pService.RawPath
	}
	return "/"
}

func (pService *proxyService) TargetPort() string {
	if pService.RawTargetPort != "" {
		return pService.RawTargetPort
	}
	return "80"
}

type proxyEndpoint struct {
	Ip string
	Port string
}

func (pEndpoint *proxyEndpoint) Endpoint() string {
	if pEndpoint.Ip != "" {
		if pEndpoint.Port != "" {
			return fmt.Sprintf("%s:%s", pEndpoint.Ip, pEndpoint.Port)
		}
	}
	return ""
}


func (kn *kube2nginx) etcdAddService(pService *proxyService) error {
	// Set with no TTL, and hope that kubernetes events are accurate.
	pathKey := fmt.Sprintf("/proxy/services/%s/path", pService.Service)
	_, err := kn.etcdClient.Set(pathKey, pService.Path(), uint64(0))
	if err != nil {
		return err
	}

	if pService.HTTP {
		httpKey := fmt.Sprintf("/proxy/domains/%s/http/services/%s", pService.Domain(), pService.Service)
		_, err := kn.etcdClient.Set(httpKey, pService.Path(), uint64(0))
		if err != nil {
			return err
		}
	}

	if pService.HTTPS {
		httpsKey := fmt.Sprintf("/proxy/domains/%s/https/services/%s", pService.Domain(), pService.Service)
		_, err := kn.etcdClient.Set(httpsKey, pService.Path(), uint64(0))
		if err != nil {
			return err
		}
	}

	return nil
}

func (kn *kube2nginx) etcdAddServiceEndpoint(pService *proxyService, pEndpoint *proxyEndpoint) error {
	// Set with no TTL, and hope that kubernetes events are accurate.
	key := fmt.Sprintf("/proxy/services/%s/endpoints/%s", pService.Service, pEndpoint.Ip)
	_, err := kn.etcdClient.Set(key, pEndpoint.Port, uint64(0))
	return err
}

func (kn *kube2nginx) etcdRemoveKey(key string) error {
	resp, err := kn.etcdClient.RawGet(key, false, true)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		glog.V(2).Infof("key %s does not exist in etcd", key)
		return nil
	}
	_, err = kn.etcdClient.Delete(key, true)
	return err
}

// Removes 'proxy service' from etcd.
func (kn *kube2nginx) etcdRemoveService(pService *proxyService) error {
	glog.V(2).Infof("Removing proxy service %+v from reverse proxy", pService)

	serviceKey := fmt.Sprintf("/proxy/services/%s", pService.Service)
	err := kn.etcdRemoveKey(serviceKey)

	httpKey := fmt.Sprintf("/proxy/domains/%s/http/services/%s", pService.Domain(), pService.Service)
	err = kn.etcdRemoveKey(httpKey)

	httpsKey := fmt.Sprintf("/proxy/domains/%s/https/services/%s", pService.Domain(), pService.Service)
	err = kn.etcdRemoveKey(httpsKey)

	return err
}

// Removes 'proxy service' from etcd.
func (kn *kube2nginx) etcdRemoveEndpoint(pService *proxyService, endpointName string) error {
	glog.V(2).Infof("Removing endpoint %s of service %+v from reverse proxy", endpointName, pService)

	endpointKey := fmt.Sprintf("/proxy/services/%s/endpoints/%s", pService.Service, endpointName)
	err := kn.etcdRemoveKey(endpointKey)
	return err
}

func  (kn *kube2nginx) etcdSyncServiceEndpoints(pService *proxyService, serviceEndpoints map[string]*proxyEndpoint) error {
	key := fmt.Sprintf("/proxy/services/%s/endpoints", pService.Service)
	resp, err := kn.etcdClient.Get(key, false, true)
	if err != nil {
		etcdErr := err.(*etcd.EtcdError)
		if etcdErr.ErrorCode != 100 {
			return err
		}
	}

	if resp != nil {
		proxyEndpoints := resp.Node.Nodes;
		for _, v := range proxyEndpoints {
			key := path.Base(v.Key)
			if serviceEndpoints[key] == nil {
				glog.V(2).Infof("Removing proxy endpoint %s for service %+v", key, pService)
				err := kn.etcdRemoveEndpoint(pService, key)
				if err != nil {
					glog.V(2).Infof("Error %s removing proxy endpoint %s for service %+v", err, key, pService)
				}
			}
		}
	}

	for _, v := range serviceEndpoints {
		glog.V(2).Infof("Adding proxy endpoint %s for service %+v", v.Endpoint(), pService)
		err := kn.etcdAddServiceEndpoint(pService, v)
		if err != nil {
			glog.V(2).Infof("Error %s adding proxy endpoint %+v for service %+v", err, v, pService)
		}
	}
	return nil
}

// Create new proxy service and sync endpoints
func (kn *kube2nginx) addProxyService(pService *proxyService, service *kapi.Service) error {
	kn.mlock.Lock()
	defer kn.mlock.Unlock()
	glog.V(2).Infof("Adding new service %s with domain %s and with path %s\n", pService.Service, pService.Domain(), pService.Path())
	err := kn.etcdAddService(pService)
	if err != nil {
		return err
	}
	key, err := kcache.MetaNamespaceKeyFunc(service)
	if err != nil {
		return err
	}
	e, exists, err := kn.endpointsStore.GetByKey(key)
	if err != nil {
		return fmt.Errorf("failed to get endpoints object from endpoints store - %v", err)
	}
	if !exists {
		glog.V(1).Infof("could not find endpoints for service %q in namespace %q. Proxy endpoints will be created once service endpoints show up.", service.Name, service.Namespace)
		return nil
	}
	if e, ok := e.(*kapi.Endpoints); ok {
		return kn.syncProxyServiceEndpoints(pService, e, service)
	}
	return nil
}

func (kn *kube2nginx) syncProxyServiceEndpoints(pService *proxyService, e *kapi.Endpoints, svc *kapi.Service) error {
	endpoints := make(map[string]*proxyEndpoint)
	for idx := range e.Subsets {
		glog.V(2).Infof("endpoints subsets for service %+v : %+v e", pService, e.Subsets)
		for subIdx := range e.Subsets[idx].Addresses {
			glog.V(2).Infof("endpoints addresses for service %+v : %+v e", pService, e.Subsets[idx].Addresses)
			endpointIp := e.Subsets[idx].Addresses[subIdx].IP

			for portIdx := range e.Subsets[idx].Ports {
				endpointPort := strconv.Itoa(e.Subsets[idx].Ports[portIdx].Port)
				if endpointPort == pService.TargetPort() {
					endpoint := proxyEndpoint{Ip: endpointIp, Port: endpointPort}
					endpoints[endpoint.Ip] = &endpoint;
				}
			}
		}
	}

	err := kn.etcdSyncServiceEndpoints(pService, endpoints)
	if err != nil {
		return err
	}
	return nil
}

func (kn *kube2nginx) getServiceFromEndpoints(e *kapi.Endpoints) (*kapi.Service, error) {
	key, err := kcache.MetaNamespaceKeyFunc(e)
	if err != nil {
		return nil, err
	}
	obj, exists, err := kn.servicesStore.GetByKey(key)
	if err != nil {
		return nil, fmt.Errorf("failed to get service object from services store - %v", err)
	}
	if !exists {
		glog.V(1).Infof("could not find service for endpoint %q in namespace %q", e.Name, e.Namespace)
		return nil, nil
	}
	if svc, ok := obj.(*kapi.Service); ok {
		return svc, nil
	}
	return nil, fmt.Errorf("got a non service object in services store %v", obj)
}

func (kn *kube2nginx) serviceToProxyService(svc *kapi.Service) *proxyService {
	annotations := svc.ObjectMeta.Annotations
	pService := &proxyService{Service: svc.ObjectMeta.Name}
	for k, v := range annotations {
		if "proxyDomain" == k {
			pService.RawDomain = v
		} else if "proxyPath" == k {
			pService.RawPath = v
		} else if "proxyHttp" == k && v == "true" {
			pService.HTTP = true
		} else if "proxyHttps" == k && v == "true" {
			pService.HTTPS = true
		} else if "proxyTargetPort" == k {
			pService.RawTargetPort = v
		}
	}

	if pService.RawDomain == "" && pService.RawPath == "" && pService.HTTP == false && pService.HTTPS == false {
		return nil
	}
	return pService
}

func (kn *kube2nginx) addProxyEndpoints(e *kapi.Endpoints) error {
	kn.mlock.Lock()
	defer kn.mlock.Unlock()
	svc, err := kn.getServiceFromEndpoints(e)
	if err != nil {
		return err
	}
	if svc == nil {
		return nil
	}

	pService := kn.serviceToProxyService(svc)
	if (pService != nil) {
		return kn.syncProxyServiceEndpoints(pService, e, svc)
	}

	return nil
}

func (kn *kube2nginx) handleEndpointAdd(obj interface{}) {
	if e, ok := obj.(*kapi.Endpoints); ok {
		kn.mutateEtcdOrDie(func() error { return kn.addProxyEndpoints(e) })
	}
}

func (kn *kube2nginx) handleServiceAdd(obj interface{}) {
	if s, ok := obj.(*kapi.Service); ok {
		pService := kn.serviceToProxyService(s)
		if pService != nil {
			kn.mutateEtcdOrDie(func() error { return kn.addProxyService(pService, s) })
		}
	}
}

func (kn *kube2nginx) handleServiceRemove(obj interface{}) {
	if s, ok := obj.(*kapi.Service); ok {
		pService := kn.serviceToProxyService(s)
		if pService != nil {
			kn.mutateEtcdOrDie(func() error { return kn.etcdRemoveService(pService) })
		}
	}
}

func (kn *kube2nginx) handleServiceUpdate(oldObj, newObj interface{}) {
	// TODO: Avoid unwanted updates.
	kn.handleServiceRemove(oldObj)
	kn.handleServiceAdd(newObj)
}

// Implements retry logic for arbitrary mutator. Crashes after retrying for
// etcd_mutation_timeout.
func (ks *kube2nginx) mutateEtcdOrDie(mutator func() error) {
	timeout := time.After(ks.etcdMutationTimeout)
	for {
		select {
		case <-timeout:
			glog.Fatalf("Failed to mutate etcd for %v using mutator: %v", ks.etcdMutationTimeout, mutator)
		default:
			if err := mutator(); err != nil {
				delay := 50 * time.Millisecond
				glog.V(1).Infof("Failed to mutate etcd using mutator: %v due to: %v. Will retry in: %v", mutator, err, delay)
				time.Sleep(delay)
			} else {
				return
			}
		}
	}
}

// Returns a cache.ListWatch that gets all changes to services.
func createServiceLW(kubeClient *kclient.Client) *kcache.ListWatch {
	return kcache.NewListWatchFromClient(kubeClient, "services", kapi.NamespaceAll, kSelector.Everything())
}

// Returns a cache.ListWatch that gets all changes to endpoints.
func createEndpointsLW(kubeClient *kclient.Client) *kcache.ListWatch {
	return kcache.NewListWatchFromClient(kubeClient, "endpoints", kapi.NamespaceAll, kSelector.Everything())
}


func newEtcdClient(etcdServer string) (*etcd.Client, error) {
	var (
		client *etcd.Client
		err    error
	)
	for attempt := 1; attempt <= maxConnectAttempts; attempt++ {
		if _, err = etcdstorage.GetEtcdVersion(etcdServer); err == nil {
			break
		}
		if attempt == maxConnectAttempts {
			break
		}
		glog.Infof("[Attempt: %d] Attempting access to etcd after 5 second sleep", attempt)
		time.Sleep(5 * time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd server: %v, error: %v", etcdServer, err)
	}
	glog.Infof("Etcd server found: %v", etcdServer)

	// loop until we have > 0 machines && machines[0] != ""
	poll, timeout := 1*time.Second, 10*time.Second
	if err := wait.Poll(poll, timeout, func() (bool, error) {
		if client = etcd.NewClient([]string{etcdServer}); client == nil {
			return false, fmt.Errorf("etcd.NewClient returned nil")
		}
		client.SyncCluster()
		machines := client.GetCluster()
		if len(machines) == 0 || len(machines[0]) == 0 {
			return false, nil
		}
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("Timed out after %s waiting for at least 1 synchronized etcd server in the cluster. Error: %v", timeout, err)
	}
	return client, nil
}

func expandKubeMasterURL() (string, error) {
	parsedURL, err := url.Parse(os.ExpandEnv(*argKubeMasterURL))
	if err != nil {
		return "", fmt.Errorf("failed to parse --kube_master_url %s - %v", *argKubeMasterURL, err)
	}
	if parsedURL.Scheme == "" || parsedURL.Host == "" || parsedURL.Host == ":" {
		return "", fmt.Errorf("invalid --kube_master_url specified %s", *argKubeMasterURL)
	}
	return parsedURL.String(), nil
}

// TODO: evaluate using pkg/client/clientcmd
func newKubeClient() (*kclient.Client, error) {
	var (
		config    *kclient.Config
		err       error
		masterURL string
	)
	// If the user specified --kube_master_url, expand env vars and verify it.
	if *argKubeMasterURL != "" {
		masterURL, err = expandKubeMasterURL()
		if err != nil {
			return nil, err
		}
	}
	if masterURL != "" && *argKubecfgFile == "" {
		// Only --kube_master_url was provided.
		config = &kclient.Config{
			Host:    masterURL,
			Version: "v1",
		}
	} else {
		// We either have:
		//  1) --kube_master_url and --kubecfg_file
		//  2) just --kubecfg_file
		//  3) neither flag
		// In any case, the logic is the same.  If (3), this will automatically
		// fall back on the service account token.
		overrides := &kclientcmd.ConfigOverrides{}
		overrides.ClusterInfo.Server = masterURL                                     // might be "", but that is OK
		rules := &kclientcmd.ClientConfigLoadingRules{ExplicitPath: *argKubecfgFile} // might be "", but that is OK
		if config, err = kclientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig(); err != nil {
			return nil, err
		}
	}

	glog.Infof("Using %s for kubernetes master", config.Host)
	glog.Infof("Using kubernetes API %s", config.Version)
	return kclient.New(config)
}

func watchForServices(kubeClient *kclient.Client, ks *kube2nginx) kcache.Store {
	serviceStore, serviceController := kframework.NewInformer(
		createServiceLW(kubeClient),
		&kapi.Service{},
		resyncPeriod,
		kframework.ResourceEventHandlerFuncs{
			AddFunc:    ks.handleServiceAdd,
			DeleteFunc: ks.handleServiceRemove,
			UpdateFunc: ks.handleServiceUpdate,
		},
	)
	go serviceController.Run(util.NeverStop)
	return serviceStore
}

func watchEndpoints(kubeClient *kclient.Client, ks *kube2nginx) kcache.Store {
	eStore, eController := kframework.NewInformer(
		createEndpointsLW(kubeClient),
		&kapi.Endpoints{},
		resyncPeriod,
		kframework.ResourceEventHandlerFuncs{
			AddFunc: ks.handleEndpointAdd,
			UpdateFunc: func(oldObj, newObj interface{}) {
				// TODO: Avoid unwanted updates.
				ks.handleEndpointAdd(newObj)
			},
		},
	)

	go eController.Run(util.NeverStop)
	return eStore
}


func main() {
	flag.Parse()
	var err error
	// TODO: Validate input flags.
	ks := kube2nginx{
		etcdMutationTimeout: *argEtcdMutationTimeout,
	}
	if ks.etcdClient, err = newEtcdClient(*argEtcdServer); err != nil {
		glog.Fatalf("Failed to create etcd client - %v", err)
	}

	kubeClient, err := newKubeClient()
	if err != nil {
		glog.Fatalf("Failed to create a kubernetes client: %v", err)
	}

	ks.endpointsStore = watchEndpoints(kubeClient, &ks)
	ks.servicesStore = watchForServices(kubeClient, &ks)

	select {}
}
