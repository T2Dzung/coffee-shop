# ADR-03: Không sử dụng Webhook và Finalizer ở phiên bản v0.1

## 0. Vấn đề
Việc thiết lập các cơ chế kiểm tra dữ liệu đầu vào (Validation) và dọn dẹp tài nguyên khi xóa CR (Cleanup) trong Kubernetes Operator có thể thực hiện theo nhiều cách. Cần lựa chọn giải pháp có độ phức tạp thấp nhất cho phiên bản v0.1 mà vẫn đảm bảo tính an toàn hệ thống và không tạo ra các điểm nghẽn vận hành (outage vectors).

## 1. Kiến thức
- **Validating/Mutating Webhook:** Các service chạy trong cluster được API server gọi để validate hoặc sửa đổi object trước khi lưu vào etcd. Yêu cầu quản lý chứng chỉ TLS (thường qua cert-manager) và tạo ra nguy cơ nghẽn API server nếu pods webhook sập.
- **OpenAPI Schema Validation / CEL (Common Expression Language):** Định nghĩa các quy tắc kiểm tra định dạng trực tiếp trên CRD schema. Kubernetes API server tự xử lý mà không cần gọi service ngoài.
- **OwnerReference:** Khai báo liên kết cha-con. Khi đối tượng cha (CR) bị xóa, Kubernetes garbage collector sẽ tự động xóa các đối tượng con (Deployment, Service) theo tầng.
- **Finalizer:** Chuỗi định danh trong metadata của object. Ngăn Kubernetes xóa đối tượng cho đến khi controller thực hiện xong các logic cleanup tùy chỉnh (custom cleanup) và gỡ bỏ finalizer.

## 2. Tư duy (Options)
- **Option A: Triển khai đầy đủ Webhook và Finalizer ngay từ v0.1**
  - *Ưu điểm:* Kiểm tra dữ liệu đầu vào cực kỳ linh hoạt (ví dụ: kiểm tra sự tồn tại của ConfigMap/Secret liên quan), kiểm soát hoàn toàn việc xóa tài nguyên con (chủ động gỡ bỏ/sao lưu trước khi xóa).
  - *Nhược điểm:* Tăng mạnh độ phức tạp hạ tầng (phải cài cert-manager, cấu hình webhook deployment, xử lý cert rotation). Nếu webhook bị lỗi, người dùng không thể tạo hoặc cập nhật bất kỳ tài nguyên nào trong cluster. Nếu controller bị kẹt, CR sẽ bị kẹt ở trạng thái deleting vô hạn do finalizer chưa được gỡ.
- **Option B: Sử dụng OpenAPI/CEL cho Validation, OwnerReference cho Cleanup (Không Webhook/Finalizer)**
  - *Ưu điểm:* Cực kỳ đơn giản, đáng tin cậy 100%. Không phụ thuộc cert-manager. Việc dọn dẹp do chính Kubernetes garbage collector đảm nhiệm thông qua `OwnerReference`, loại bỏ nguy cơ kẹt xóa CR.
  - *Nhược điểm:* Không thực hiện được các kiểm tra logic động phức tạp (không thể kiểm tra chéo các tài nguyên khác tại thời điểm nhận request).

## 3. Quyết định
**Chọn Option B — Sử dụng OpenAPI/CEL cho Validation, OwnerReference cho Cleanup ở phiên bản v0.1.**
- **Lý do:** Giảm thiểu blast radius và điểm lỗi (single point of failure). Các quy định về format cổng, số lượng replicas, requests/limits hoàn toàn có thể biểu diễn được bằng OpenAPI validation marker của Kubebuilder hoặc quy tắc CEL trực tiếp trên struct. Vì các tài nguyên con đều là namespaced và thuộc sở hữu của CR, cơ chế `OwnerReference` mặc định là đủ an toàn để tự động xóa sạch tài nguyên con khi xóa CR.
- **Trade-off:** Chấp nhận không kiểm tra động (như kiểm tra xem Secret được tham chiếu có thực sự tồn tại hay không). Logic này sẽ được kiểm tra một cách phòng thủ (defensively) trong controller reconcile loop và báo cáo qua `.status.conditions` (ví dụ: `DependencyInvalid`) thay vì reject request tại API server.

## 4. Kiểm chứng
- Khi định nghĩa struct `CoffeeShopServiceSpec`, sử dụng các marker của Kubebuilder như `// +kubebuilder:validation:Minimum=1`, `// +kubebuilder:validation:Pattern=...`
- Kiểm thử tạo CRD, kiểm tra xem API server có tự động từ chối khi truyền spec sai định dạng không.
- Kiểm thử xóa CR, xác nhận Deployment và Service con tương ứng tự động biến mất nhờ OwnerReference.
