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
  data:
    - secretKey: username
      remoteRef:
        key: __RDS_MASTER_SECRET_ARN__
        property: username
    - secretKey: password
      remoteRef:
        key: __RDS_MASTER_SECRET_ARN__
        property: password
    - secretKey: host
      remoteRef:
        key: __RDS_MASTER_SECRET_ARN__
        property: host
    - secretKey: port
      remoteRef:
        key: __RDS_MASTER_SECRET_ARN__
        property: port
