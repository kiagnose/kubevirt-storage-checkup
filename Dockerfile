FROM registry.access.redhat.com/ubi9/ubi-minimal:9.1.0-1656.1669627757

RUN microdnf install -y shadow-utils && \
    adduser --system --no-create-home -u 900 storage-checkup && \
    microdnf remove -y shadow-utils && \
    microdnf clean all

COPY ./bin/kubevirt-storage-checkup /usr/local/bin

USER 900

ENTRYPOINT ["kubevirt-storage-checkup"]
