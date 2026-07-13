# Migration Contract Skeleton — Raw Manifests sang CoffeeShopService

Trạng thái: **Design-only trong Phase 6.0–6.1; không được thực thi trên dev cluster**.

Reconciler hiện read-only. Adoption, SSA, status patch và child watches thuộc Phase 6.2. Tài liệu này chỉ khóa invariants để Phase 6.2 không tạo migration gây downtime.

## Invariants

- ArgoCD quản lý CR; operator quản lý generated children sau cutover.
- Không để ArgoCD và operator đồng thời sở hữu cùng fields.
- `Observe` không được ghi child resource.
- Existing same-name child mặc định bị reject.
- Adoption yêu cầu CR `adoptionPolicy=Explicit` và child annotation double opt-in.
- Preflight phải so sánh selectors, ports, image, resources, probes, HPA và Argo tracking trước khi đổi owner.
- Không xóa raw manifests hoặc bật prune trước khi ownership handoff được xác minh.

## State machine mục tiêu cho Phase 6.2

```text
Raw Git-managed
  -> CR Observe + adoption Never
  -> preflight/diff only
  -> CR Observe + adoption Explicit + child annotation
  -> explicit adoption operation
  -> verify owner/field ownership, workload readiness and smoke tests
  -> CR Manage
  -> remove raw child manifests from Argo source
  -> re-enable sync/prune
```

## Evidence bắt buộc trước pilot

- raw và desired selector/port contracts giống nhau;
- không có HPA target Deployment;
- HTTPRoute vẫn resolve Service cùng tên;
- workload ít nhất hai replicas nếu dùng PDB;
- rollout/smoke test pass;
- rollback rehearsal pass trên Kind;
- API group/version khớp `platform.t2dzung.github.io/v1alpha1`.

Các lệnh mutate ownerReference sẽ chỉ được thêm sau khi Phase 6.2 implementation và tests chứng minh state machine này.
