# ADR-04: Sử dụng Standard Kubernetes NetworkPolicy cho Kết nối giữa các Service
> Implementation status: Deferred to Phase 6.4; Phase 6.1 contains no child writes.

## 0. Vấn đề
Khi tự động hóa việc tạo NetworkPolicy từ khai báo phụ thuộc (`spec.networkPolicy.egress`) trong CR `CoffeeShopService`, cần lựa chọn loại tài nguyên NetworkPolicy phù hợp để bảo vệ giao thông mạng giữa các service stateless của CoffeeShop mà không làm ảnh hưởng đến khả năng tương thích của hệ thống.

## 1. Kiến thức
- **Standard Kubernetes NetworkPolicy (`networking.k8s.io/v1`):** API tiêu chuẩn được hỗ trợ bởi hầu hết các CNI (Cilium, Calico, Flannel+Canal, v.v.). Cho phép cấu hình rules dựa trên label selector của Pod và Namespace.
- **CiliumNetworkPolicy (CRD của Cilium):** API nâng cao riêng của Cilium CNI, hỗ trợ các tính năng mạnh mẽ như Layer 7 rules (HTTP path/method), DNS-based egress, và Cilium Entities.

## 2. Tư duy (Options)
- **Option A: Sử dụng CiliumNetworkPolicy CRD**
  - *Ưu điểm:* Cho phép phân tách kết nối ở mức rất sâu (ví dụ: chỉ cho phép `proxy` gọi HTTP POST `/orders` tới `counter`, chặn các path khác). Hỗ trợ egress trỏ tới DNS domain bên ngoài trực tiếp trong policy.
  - *Nhược điểm:* Khóa chặt (lock-in) operator vào Cilium CNI. Nếu trong tương lai chạy trên một hạ tầng khác không dùng Cilium (ví dụ: AWS EKS dùng AWS VPC CNI mặc định và security groups), operator sẽ không hoạt động được do thiếu CRD Cilium.
- **Option B: Sử dụng Standard Kubernetes NetworkPolicy**
  - *Ưu điểm:* Độc lập hoàn toàn với CNI. Hoạt động tốt trên cả K3s dev (chạy Cilium) và AWS EKS (chạy AWS VPC CNI hoặc CNI khác). Dễ dàng cài đặt và kiểm thử.
  - *Nhược điểm:* Không hỗ trợ các rule Layer 7 hoặc DNS-based egress ở mức operator. Các luật nâng cao này (như cho phép egress tới AWS S3, ECR) phải được quản lý thủ công qua GitOps ngoài luồng operator.

## 3. Quyết định
**Chọn Option B — Sử dụng Standard Kubernetes NetworkPolicy.**
- **Lý do:** Đảm bảo tính di động (portable) cao của hạ tầng từ môi trường giả lập Dev K3s lên AWS EKS Production. Trách nhiệm của operator chỉ giới hạn ở việc tự động hóa các đường kết nối app-to-app cơ bản trong cùng namespace (ví dụ: `proxy -> product`).
- **Trade-off:** Chấp nhận rằng các chính sách mạng phức tạp khác (như kết nối ra internet, kết nối tới AWS S3, hoặc kết nối tới PostgreSQL/RabbitMQ của hệ thống) sẽ tiếp tục được quản lý dưới dạng các tệp Manifest tĩnh trong Git (Git-managed) thay vì do operator sinh ra.

## 4. Kiểm chứng
- Operator sử dụng API nhóm `networking.k8s.io/v1` để tạo tài nguyên `NetworkPolicy`.
- Kiểm thử e2e: Apply CR `proxy` có khai báo egress tới `product`. Xác nhận 2 đối tượng `NetworkPolicy` tiêu chuẩn được tạo ra (một egress policy cho `proxy` và một ingress policy cho `product`).
- Hubble CLI của Cilium hiển thị traffic giữa các pods khớp chính xác với luật được định nghĩa bởi các NetworkPolicy tiêu chuẩn này.
