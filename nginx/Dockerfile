FROM debian:jessie

MAINTAINER Lineberty "itadmin@lineberty.com"

COPY ./confd-0.10.0-linux-amd64 /usr/bin/confd
COPY ./docker-entrypoint.sh /
COPY ./confd /etc/confd

RUN apt-key adv --keyserver hkp://pgp.mit.edu:80 --recv-keys 573BFD6B3D8FBC641079A6ABABF5BD827BD9BF62
RUN echo "deb http://nginx.org/packages/mainline/debian/ jessie nginx" >> /etc/apt/sources.list

ENV NGINX_VERSION 1.9.4-1~jessie

RUN apt-get update && \
    apt-get install -y ca-certificates nginx=${NGINX_VERSION} && \
    rm -rf /var/lib/apt/lists/*

# forward request and error logs to docker log collector
RUN ln -sf /dev/stdout /var/log/nginx/access.log
RUN ln -sf /dev/stderr /var/log/nginx/error.log

RUN rm -r /etc/nginx
COPY ./nginx /etc/nginx

RUN chmod +x /usr/bin/confd

EXPOSE 80 443

ENTRYPOINT ["/docker-entrypoint.sh"]
CMD ["nginx"]