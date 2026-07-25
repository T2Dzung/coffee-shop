apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: coffeeshop-rds-master-bootstrap
  namespace: coffeeshop
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-secretsmanager
    kind: ClusterSecretStore
  target:
    name: coffeeshop-rds-master-bootstrap
    creationPolicy: Owner
    template:
      engineVersion: v2
      data:
        username: "{{ .username }}"
        password: "{{ .password }}"
        host: "__RDS_ADDRESS__"
        port: "__RDS_PORT__"
  data:
    - secretKey: username
      remoteRef:
        key: __RDS_MASTER_SECRET_ARN__
        property: username
    - secretKey: password
      remoteRef:
        key: __RDS_MASTER_SECRET_ARN__
        property: password
