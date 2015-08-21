#!/bin/bash
set -e

echo "Booting container ..."
if [ "$1" = 'nginx' ]; then
    if [ -d /docker-entrypoint.d ]; then
        for f in /docker-entrypoint.d/*.sh; do
            echo "Executing entrypoint $f ..."
            [ -f "$f" ] && . "$f"
        done
    fi

    cd /usr/share/nginx/html
    export ETCD_ADVERTISE_CLIENT_URLS=${ETCD_ADVERTISE_CLIENT_URLS:-http://172.17.42.1:4001}

    echo "confd is now monitoring etcd ($ETCD_ADVERTISE_CLIENT_URLS) for changes ..."
    confd -node $ETCD_ADVERTISE_CLIENT_URLS &

    echo "Starting nginx service ..."
    /usr/sbin/nginx -g "daemon off;"
else
    exec "$@"
fi