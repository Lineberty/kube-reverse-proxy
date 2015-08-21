# kube2sky
==============

This is based on kube2dns work in kubernetes

A bridge between Kubernetes and Nginx / Conf.  This will watch the kubernetes API for
changes in Services and then add reverse proxy entries for services with annotations

For now, this is expected to be run in a pod alongside the etcd and Confd/Nginx
containers.

## Service usage
To add a reverse proxy entry, you should the following keys to the annotations of the service :
* proxyDomain : Domain used. By default it's _ which is anyy domain
* proxyPath : http path to the service
* proxyHttp : Reverse proxy through http
* proxyHttps : Reverse proxy through https

For https, a certificates and a key named as domain_name.com.crt and domain_name.com.key shoud be added
to a kubernetes secret volume at /etc/secret

## Flags

`-verbose`: Log additional information.

`-etcd_mutation_timeout`: For how long the application will keep retrying etcd 
mutation (insertion or removal of a dns entry) before giving up and crashing.

`--etcd-server`: The etcd server that is being used by skydns.

`--kube_master_url`: URL of kubernetes master. Required if `--kubecfg_file` is not set.

`--kubecfg_file`: Path to kubecfg file that contains the master URL and tokens to authenticate with the master.
