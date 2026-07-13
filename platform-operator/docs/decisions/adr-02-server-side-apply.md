# ADR-02: Sử dụng Server-Side Apply (SSA) làm cơ chế Reconcile chính
> Implementation status: Deferred to Phase 6.2; Phase 6.1 contains no child writes.

## 0. Vấn đề
Khi viết controller, việc thay đổi hoặc đồng bộ hóa các child resources (Deployment, Service, PDB, NetworkPolicy) thường gặp xung đột nếu có các thành phần khác (như ArgoCD, HPA, hoặc Mutating Webhook của bên thứ ba) cũng chỉnh sửa các tài nguyên này. Cần một cơ chế an toàn để cập nhật tài nguyên con mà không đè lên các thay đổi ngoài ý muốn.

## 1. Kiến thức
- **Full Object Update (`Update()`):** Lấy trạng thái hiện tại từ cache, sửa đổi và gửi toàn bộ object lên API server. Nếu object trên server đã bị thay đổi kể từ khi lấy ra, API server sẽ trả về lỗi Conflict (HTTP 409) và yêu cầu retry. Phương pháp này dễ ghi đè các cấu hình do controller khác hoặc admission webhook inject vào (như sidecar container, annotation).
- **Server-Side Apply (SSA):** Gửi một bản patch dạng YAML/JSON chỉ chứa các trường (fields) mà controller này thực sự sở hữu. API server sẽ thực hiện merge ở phía server dựa trên cấu trúc `ManagedFields`.

## 2. Tư duy (Options)
- **Option A: Dùng Update/Patch client-side thông thường**
  - *Ưu điểm:* Dễ cài đặt bằng thư viện mặc định của controller-runtime, có nhiều ví dụ mẫu trực tuyến để tham khảo.
  - *Nhược điểm:* Dễ gây ra lỗi conflict liên tục dưới tải cao, hoặc vô tình xóa mất các annotations/labels được ArgoCD hoặc các service mesh/observability injectors gắn vào pod/deployment.
- **Option B: Sử dụng Server-Side Apply (SSA) với field manager `coffeeshop-operator`**
  - *Ưu điểm:* Xác định rõ ràng quyền sở hữu trường (field ownership). Nếu có xung đột (conflict), API server sẽ từ chối và báo rõ ràng. Hỗ trợ việc cùng quản lý (co-management) an toàn với ArgoCD.
  - *Nhược điểm:* API client viết phức tạp hơn do phải sử dụng cấu trúc patch và quản lý conflict logic.

## 3. Quyết định
**Chọn Option B — Sử dụng Server-Side Apply với field manager cố định là `coffeeshop-operator`.**
- **Lý do:** Đây là tiêu chuẩn hiện đại (production-grade) của Kubernetes API giúp phân tách quyền hạn rõ ràng. Tránh việc "giành giật" cấu hình (controller fighting) giữa operator và ArgoCD.
- **Trade-off:** Cần xử lý conflict tường minh. Nếu có conflict thực tế xảy ra (ví dụ: người dùng sửa tay số lượng replicas trực tiếp trên Deployment thay vì trên CR), operator sẽ không ép buộc ghi đè ngay ở steady state (không set `Force: true`), mà sẽ ghi nhận trạng thái lỗi `ApplyConflict` vào status condition và requeue có backoff.

## 4. Kiểm chứng
- Lệnh apply của operator sử dụng:
  ```go
  client.Patch(ctx, obj, client.Apply, client.FieldOwner("coffeeshop-operator"))
  ```
- Kiểm thử tạo Deployment, sau đó thêm label/annotation thủ công bằng `kubectl`. Xác nhận operator reconcile tiếp theo không làm mất các label/annotation thủ công đó.
