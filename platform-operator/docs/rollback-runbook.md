# Rollback Contract Skeleton — CoffeeShopService về Raw Manifests

Trạng thái: **Design-only trong Phase 6.0–6.1**.

Không dùng quy trình “xóa CR rồi apply YAML”. OwnerReference có thể khiến garbage collector xóa Deployment/Service và tạo downtime.

## Invariants

1. Chuyển CR sang `Observe` để dừng child writes.
2. Chuẩn bị raw manifests cùng name/selectors và kiểm tra server-side dry-run.
3. Chuyển ownership trước khi xóa CR; không phụ thuộc race với garbage collector.
4. Verify workload ready và smoke path trước khi re-enable Argo prune.
5. Chỉ xóa CR sau khi child không còn controller owner reference tới CR.
6. Không xóa CRD trong rollback workload; CRD removal là lifecycle riêng và có thể xóa toàn bộ CRs.

## Evidence cần có ở Phase 6.2+

- workload không xuống dưới availability budget;
- Service ClusterIP/HTTPRoute path không bị thay đổi ngoài kế hoạch;
- ArgoCD trở lại Synced/Healthy;
- operator không còn managedFields trên raw-managed intent;
- rollback timing và smoke output được lưu trong implementation evidence.
