# No registry base or compilation is needed. Runtime dependencies are embedded.
FROM scratch
ARG TARGETARCH=amd64
COPY --chmod=755 bin/tracework-linux-${TARGETARCH} /tracework
COPY --chown=10001:10001 docker-data/ /data/
USER 10001:10001
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/tracework"]
CMD ["serve", "--listen", "0.0.0.0:8080", "--data", "/data"]
