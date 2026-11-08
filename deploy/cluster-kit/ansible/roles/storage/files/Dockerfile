FROM postgres:18.1-bookworm
RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 python3-venv ca-certificates \
    && python3 -m venv /opt/patroni \
    && /opt/patroni/bin/pip install --no-cache-dir 'patroni[etcd3,psycopg3]==4.1.5' 'psycopg[binary]==3.2.3' \
    && /opt/patroni/bin/pip check \
    && rm -rf /var/lib/apt/lists/*
COPY bootstrap.py /opt/bootstrap.py
ENV PATH=/opt/patroni/bin:$PATH
USER 999:999
ENTRYPOINT ["/opt/patroni/bin/patroni"]
CMD ["/config/patroni.yml"]
