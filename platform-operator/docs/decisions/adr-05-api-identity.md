# ADR-05: API identity dùng namespace `t2dzung.github.io`

- Trạng thái: Accepted
- Ngày: 2026-07-10
- Phạm vi: Phase 6.0

## 0. Vấn đề

API group là identity dài hạn của CRD. Plan cũ dùng `platform.coffeeshop.io`, nhưng repository không có evidence kiểm soát `coffeeshop.io`. Phát hành API dưới DNS suffix không kiểm soát tạo nguy cơ collision và làm migration sau release phức tạp.

## 1. Kiến thức

Kubernetes khuyến nghị API group dùng DNS-style name thuộc namespace mà project kiểm soát. API group được nhúng vào CRD name, RBAC, manifests, discovery và persisted objects; đổi group không phải rename file đơn giản.

Repository và Go module thuộc GitHub identity `T2Dzung`. GitHub cấp namespace user-site duy nhất `t2dzung.github.io` cho account đó.

## 2. Options

### A. Giữ `platform.coffeeshop.io`

- Ưu: ngắn và hợp domain nghiệp vụ.
- Nhược: không có ownership evidence; fail gate 6.0.

### B. Dùng `platform.t2dzung.github.io`

- Ưu: gắn với identity kiểm soát repository; tránh giả định sở hữu domain thương mại.
- Nhược: dài hơn và gắn API identity với GitHub account.

### C. Mua custom domain rồi scaffold lại

- Ưu: identity chuyên nghiệp và độc lập với GitHub.
- Nhược: thêm chi phí và chặn learning phase không cần thiết.

## 3. Quyết định

Chọn B trước release đầu tiên:

```text
platform.t2dzung.github.io/v1alpha1
```

`PROJECT` và group metadata được tạo từ Kubebuilder scaffold tạm với `--domain t2dzung.github.io`, không sửa generated metadata tùy tiện.

## 4. Kiểm chứng

- CRD name: `coffeeshopservices.platform.t2dzung.github.io`.
- Envtest Kubernetes 1.35 cài CRD thành công.
- Samples, RBAC và Go scheme dùng cùng group.
- Không còn `platform.coffeeshop.io` trong operator package.

Nếu sau này chuyển sang custom corporate domain, phải coi đó là API migration: serve song song hoặc cung cấp export/import procedure; không đổi group âm thầm.
