# PROD golden-journey alert runbook

This runbook handles the `coffeeshop-prod-alarm-golden-journey` alarm. The canary makes
one read-only request every five minutes:

```text
GET /api/v1/api/item-types
  -> public ALB
  -> proxy
  -> product
  -> HTTP 2xx and non-empty itemTypes JSON
```

The alarm fires when at least two of three datapoints are below 100 percent. Missing
datapoints are treated as breaching. O2 has no SNS or paging integration; the alarm is
an operating exercise, not evidence of a staffed on-call service or an external SLA.

## 1. Confirm the symptom

Use the reviewed PROD AWS identity and Region:

```bash
aws sts get-caller-identity
aws cloudwatch describe-alarms \
  --alarm-names coffeeshop-prod-alarm-golden-journey \
  --region ap-southeast-1
aws synthetics get-canary \
  --name coffeeshop-prod-api \
  --region ap-southeast-1
aws synthetics get-canary-runs \
  --name coffeeshop-prod-api \
  --max-results 10 \
  --region ap-southeast-1
```

Record the alarm transition time, state reason, recent run states and the exact source
revision. Do not record response bodies, request headers, credentials or Kubernetes
Secret values.

## 2. Separate measurement failure from service failure

| Evidence | Classification | Next step |
| --- | --- | --- |
| No scheduled run or no `SuccessPercent` datapoint | Measurement path failure | Inspect canary state, execution role, Lambda logs and artifact-bucket permissions |
| Canary ran and reports HTTP, timeout, DNS or JSON assertion failure | Service journey failure | Inspect ALB target health and proxy/product runtime |
| Canary reports failure but an independent content-aware GET succeeds | Possible measurement/configuration failure | Compare `TARGET_URL`, `MIN_ITEM_TYPES`, runtime version and canary logs |
| Canary and independent GET both fail | Confirmed synthetic journey failure | Continue through ALB and workload triage |

An ALB target marked healthy proves only that its shallow `/healthz` endpoint responds.
It does not prove that proxy can call product or that the response body satisfies the
public API contract.

## 3. Run the read-only platform check

```bash
bash scripts/platformctl.sh prod status
```

`prod status` is read-only. It requires both web and proxy target groups to contain the
corresponding Ready Pod IPs, then validates the public `item-types` response. It does not
create an order. Stateful transaction verification belongs to `prod setup`,
`prod reconcile` and the explicit resilience flow.

If the command fails, keep its first failure boundary. Do not bypass an unhealthy target
group just because the ALB still returns traffic when every target is unhealthy.

## 4. Triage by dependency boundary

### ALB and Kubernetes endpoint identity

```bash
kubectl get ingress coffeeshop-prod-alb-ingress -n coffeeshop
kubectl get service web proxy -n coffeeshop
kubectl get endpointslice -n coffeeshop -l kubernetes.io/service-name=proxy
kubectl get pods -n coffeeshop -l app=proxy -o wide
kubectl get pods -n coffeeshop -l app=product -o wide
```

Confirm that the proxy Service annotation and readiness contract both use `/healthz`.
The ALB proxy target group must report the same IPs as Ready proxy Pods.

### Proxy and product execution

```bash
kubectl logs -n coffeeshop deployment/proxy --since=15m
kubectl logs -n coffeeshop deployment/product --since=15m
kubectl rollout status -n coffeeshop deployment/proxy
kubectl rollout status -n coffeeshop deployment/product
```

Look for connection, timeout, route and serialization errors around the alarm time.
Do not paste full responses or environment variables into an issue or evidence file.

### Canary execution path

Use the Lambda log group whose name begins with
`/aws/lambda/cwsyn-coffeeshop-prod-api-`. Check for runtime, IAM, S3 upload, DNS and
timeout errors. The execution role is intentionally limited to its artifact prefix,
Lambda logs and the `CloudWatchSynthetics` metric namespace.

## 5. Mitigate through source of truth

Choose the smallest reversible source change that addresses the confirmed boundary:

- revert a bad immutable application digest through the existing rollback flow;
- repair the Kustomize Deployment, Service or Ingress contract and let Argo CD reconcile;
- repair Terraform-owned canary/IAM/S3 configuration with a reviewed saved plan;
- if the controlled-negative fixture is active, restore `slo_minimum_item_types = 1`
  and run `platformctl prod reconcile`.

Do not edit the ALB target group, canary, alarm, Deployment or Service in a console to
make the alarm green. An emergency live patch must be backported to Git and recorded in
the private break-glass log.

## 6. Verify recovery

Recovery requires all of the following:

1. `platformctl prod status` passes;
2. web and proxy target groups match their Ready Pod IPs;
3. the canary records successful runs again;
4. the alarm transitions back to `OK` for real canary datapoints;
5. Argo CD reports the platform and application sources Synced and Healthy.

Record what failed, the source revision that repaired it, the alarm transition and any
unverified dependency. A short O2 evidence window proves the mechanism and transition;
it does not prove 24-hour SLO compliance.

## 7. End the bounded evidence window

Set `slo_runtime_enabled = false` in the ignored PROD tfvars, review the saved cleanup
plan and run `platformctl prod reconcile`. Confirm that no canary remains scheduled and
that the canary alarm, dashboard, execution role and generated Lambda resources are
gone. The encrypted, public-blocked artifact bucket is retained and its objects expire
through lifecycle policy.
