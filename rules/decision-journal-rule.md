---
trigger: always_on
---

# DECISION JOURNAL RULE

> Áp dụng cho TẤT CẢ tasks trong project. Mục đích: học sâu, không copy-paste, sẵn sàng interview.

---

## Nguyên tắc cốt lõi

**Mọi quyết định kỹ thuật quan trọng phải được ghi lại theo framework 5 bước.**

Đây không phải bureaucracy — đây là cách Senior Engineer tư duy. Interviewer luôn hỏi **"tại sao?"**, không phải "cái gì?". Framework này giúp bạn trả lời tự tin.

---

## Framework 5 bước

```
┌─────────────────────────────────────────────────────────────────┐
│                    DECISION JOURNAL FRAMEWORK                    │
├──────┬──────────────────────────────────────────────────────────┤
│ Bước │ Mô tả                                                    │
├──────┼──────────────────────────────────────────────────────────┤
│  0   │ VẤN ĐỀ      Đang giải quyết bài toán gì? Tại sao       │
│      │              quan trọng? Hậu quả nếu không giải quyết?   │
├──────┼──────────────────────────────────────────────────────────┤
│  1   │ KIẾN THỨC    Các concept đơn lẻ liên quan. Tra cứu       │
│      │              docs, best practices, so sánh các tools.     │
├──────┼──────────────────────────────────────────────────────────┤
│  2   │ TƯ DUY       Kết hợp kiến thức thành các giải pháp       │
│      │              khả thi. Liệt kê ≥ 2 options.               │
├──────┼──────────────────────────────────────────────────────────┤
│  3   │ QUYẾT ĐỊNH   Chọn giải pháp. Ghi rõ trade-off:           │
│      │              "Chọn A vì ..., chấp nhận nhược điểm ..."   │
├──────┼──────────────────────────────────────────────────────────┤
│  4   │ KIỂM CHỨNG   Verify kết quả. So sánh với kỳ vọng.        │
│      │              Ghi lại metrics hoặc kết quả cụ thể.        │
└──────┴──────────────────────────────────────────────────────────┘
```

### Mỗi bước giải quyết 1 câu hỏi interview:

| Bước | Câu hỏi interview | Ví dụ trả lời |
|---|---|---|
| 0. Vấn đề | "Tại sao cần làm cái này?" | "Deploy thủ công dễ sai, không rollback được" |
| 1. Kiến thức | "Anh biết những gì về X?" | "Có 3 loại CD: manual, push-based, pull-based (GitOps)" |
| 2. Tư duy | "Có những cách nào?" | "Jenkins CD (push) vs ArgoCD (pull) vs Flux (pull)" |
| 3. Quyết định | "Tại sao chọn X thay vì Y?" | "ArgoCD vì có UI + Argo Rollouts, trade-off: nặng hơn Flux" |
| 4. Kiểm chứng | "Kết quả thực tế?" | "Auto-sync < 30s, self-heal khi xóa pod" |

---

## Khi nào cần viết Decision Journal?

**CẦN viết** cho các quyết định:
- Chọn tool/framework (ArgoCD vs Flux, Loki vs ELK, k6 vs Locust)
- Chọn architecture pattern (canary vs blue-green, push vs pull CD)
- Chọn cấu hình quan trọng (distroless vs alpine, Network Policy rules)
- Giải quyết trade-off rõ ràng (cost vs performance, security vs convenience)

**KHÔNG CẦN viết** cho:
- Boilerplate code (YAML syntax, copy từ docs)
- Quyết định hiển nhiên (dùng Go cho Go project)
- Config values cụ thể (port 8080, replicas 2)

---

## Format ghi chép

### Option A: Inline comment trong code

```yaml
# ┌─ DECISION: Chọn Loki thay ELK cho log aggregation
# │ VẤN ĐỀ:   Cần centralized logging cho K8s pods
# │ OPTIONS:   ELK (full-text index) vs Loki (label-based index)
# │ CHỌN:      Loki — nhẹ 10x (256MB vs 4GB), native Grafana
# │ TRADE-OFF: Không full-text search, phải query bằng LogQL
# └─ VERIFY:   Loki chạy OK trên Kind với 256MB RAM limit
```

### Option B: ADR file trong `docs/decisions/`

```markdown
# ADR-003: Chọn Loki thay ELK cho Log Aggregation

## 0. Vấn đề
Cần centralized logging cho 4 Go microservices trên K8s.
Hiện tại chỉ có Sentry (error tracking), không có log search.

## 1. Kiến thức
- ELK (Elasticsearch + Logstash + Kibana): Full-text index, powerful query
- EFK (Elasticsearch + Fluentd + Kibana): Fluentd nhẹ hơn Logstash
- Loki + Grafana: Chỉ index labels, query bằng LogQL

## 2. Tư duy
- ELK: Mạnh nhất nhưng cần 4-8GB RAM → Kind cluster chết
- EFK: Nhẹ hơn Logstash nhưng Elasticsearch vẫn nặng
- Loki: Chỉ cần 256MB, tích hợp native Grafana (đã có cho metrics)

## 3. Quyết định
→ Chọn Loki
- Lý do: nhẹ 10x, Grafana unified (metrics + logs + traces cùng UI)
- Trade-off: không full-text search (phải biết label/pod name trước)
- Chấp nhận được vì: K8s environment có structured labels sẵn

## 4. Kiểm chứng
- Loki chạy trên Kind: 256MB RAM, start < 10s
- Query logs theo pod: `{namespace="edtech", app="fee-service"}`
- Correlated với Tempo traces qua traceID
```

---

## Quy tắc cho AI assistant (Antigravity)

Khi triển khai code cho project này, Antigravity PHẢI:

1. **Trước khi viết code** — giải thích Decision Journal (ít nhất bước 0, 2, 3)
2. **Khi có nhiều options** — liệt kê ≥ 2 và giải thích trade-off
3. **Sau khi hoàn thành** — ghi lại kết quả kiểm chứng
4. **Đề xuất tạo ADR file** cho mỗi quyết định lớn (tool choice, architecture pattern)
5. **KHÔNG copy-paste** code mà không giải thích tại sao viết như vậy
