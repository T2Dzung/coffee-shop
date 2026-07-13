# ADR-01: Monorepo Module Layout cho Custom Operator

## 0. Vấn đề
Custom Operator dành riêng cho CoffeeShop cần một vị trí lưu trữ mã nguồn và tài liệu trong hệ thống. Cần quyết định cấu trúc thư mục để đảm bảo ranh giới mã nguồn rõ ràng, dễ bảo trì, tích hợp CI/CD và đồng bộ hóa với quá trình tiến hóa của mã nguồn ứng dụng và tệp cấu hình GitOps.

## 1. Kiến thức
Có hai mô hình quản lý repository phổ biến:
- **Monorepo (Thư mục con trong repo chính):** Toàn bộ code ứng dụng, manifests và operator nằm chung một repo. Phân tách logic bằng thư mục và Go module độc lập.
- **Multi-repo (Repository riêng biệt):** Operator nằm ở repo riêng, độc lập hoàn toàn với vòng đời của ứng dụng chính.

## 2. Tư duy (Options)
- **Option A: Tách repo riêng biệt cho platform-operator**
  - *Ưu điểm:* Ranh giới bảo mật tuyệt đối, release cadence độc lập, build CI/CD nhanh và độc lập.
  - *Nhược điểm:* Quá trình phát triển gặp overhead lớn do phải liên tục cập nhật phiên bản thư viện giữa các repo, khó kiểm thử tích hợp (integration tests) và đồng bộ tệp Manifest (như CRD, mẫu CR) với GitOps repository chính.
- **Option B: Để trong monorepo tại thư mục `platform-operator/` với Go module riêng**
  - *Ưu điểm:* Thuận tiện cho việc phát triển và đồng bộ hóa. Khi API của ứng dụng hoặc tệp Manifest thay đổi, ta có thể sửa đổi cả app code và operator trong cùng một commit. Dễ viết kiểm thử e2e với Kind sử dụng trực tiếp các manifests của app trong repo.
  - *Nhược điểm:* Kích thước repo tăng, CI/CD của repo chính có thể bị kích hoạt không cần thiết nếu không cấu hình path-filtering (chỉ chạy test operator khi thay đổi trong thư mục `platform-operator/`).

## 3. Quyết định
**Chọn Option B — Đặt tại thư mục `platform-operator/` trong monorepo với Go module riêng.**
- **Lý do:** Operator này là domain-specific (chỉ phục vụ riêng cho các service của CoffeeShop), không phải reusable generic operator. Việc đặt chung monorepo giúp đồng bộ hóa các tệp CRD, mẫu CR và config GitOps (`infrastructure/k8s/gitops`) dễ dàng hơn.
- **Trade-off:** Chấp nhận cấu hình thêm path-filtering trong GitHub Actions để tránh chạy chồng chéo test.

## 4. Kiểm chứng
- Khởi tạo thành công thư mục `platform-operator/` chứa `go.mod` riêng với tên module `github.com/T2Dzung/coffee-shop/platform-operator` (hoặc tương tự theo module chính).
- CI/CD của GitHub Actions chạy độc lập dựa trên điều kiện `paths: ['platform-operator/**']`.
