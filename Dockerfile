FROM scratch

COPY mini-metrics /
VOLUME /tmp

ENTRYPOINT ["/mini-metrics", "--port=8080"]

EXPOSE 8080

