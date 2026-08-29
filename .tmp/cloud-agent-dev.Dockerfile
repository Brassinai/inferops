FROM scratch
COPY inferops-cloud-agent /inferops-cloud-agent
USER 65532:65532
ENTRYPOINT ["/inferops-cloud-agent"]
