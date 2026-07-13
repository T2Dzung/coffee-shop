# ADR-06: Compatibility policy cho `v1alpha1`

- Trạng thái: Accepted
- Ngày: 2026-07-10
- Phạm vi: Phase 6.0–6.1

## 0. Vấn đề

CRD schema là public contract được lưu trong etcd và được GitOps clients sử dụng. Alpha không có nghĩa là có thể rename/reuse field tùy ý sau mỗi release.

## 1. Kiến thức

Một CRD có thể serve nhiều version nhưng chỉ có một storage version tại một thời điểm. Đổi `storage: true` không tự rewrite object cũ. Conversion webhook chỉ cần khi representation giữa versions khác nhau.

## 2. Options

- Breaking-in-place: nhanh nhưng phá manifests và rollback.
- Additive-first trong `v1alpha1`: chậm hơn một chút nhưng migration và rollback rõ ràng.
- Tạo version mới cho mọi thay đổi: an toàn nhưng quá nặng ở giai đoạn đầu.

## 3. Quyết định

Trong `v1alpha1`:

- ưu tiên thêm field optional;
- không rename, xóa hoặc reuse field với meaning khác;
- không đổi default khiến object cũ đổi behavior;
- selector/name identity được coi là immutable contract;
- không thêm webhook nếu OpenAPI/default/CEL biểu diễn được rule;
- thay đổi representation không tương thích phải tạo `v1beta1` hoặc API group migration rõ ràng.

`v1alpha1` hiện `served=true`, `storage=true`, không conversion webhook.

## 4. Kiểm chứng

Generated CRD được kiểm tra trong envtest và kubeconform. Mỗi change API phải chạy generated-drift check và test create/update từ fixture release trước khi có release N-1.
