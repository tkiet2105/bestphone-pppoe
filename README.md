# bestphone-pppoe

PPPoE multi-line proxy gateway cho Debian 12.

- Mỗi PPPoE session = 1 listener SOCKS5/HTTP riêng (port từ pool 30000–40000).
- Outbound socket bind vào iface `ppp<N>` qua `SO_BINDTODEVICE` + peer config `nodefaultroute` → **tắt WAN vẫn truy cập được proxy**.
- Mỗi proxy hỗ trợ **nhiều credentials** (user/pass) cùng lúc.
- REST API đầy đủ + endpoint export proxy text nhanh (`/proxies/export`).
- UI vanilla JS (no build step).
- Auto-update qua file `VERSION` (bump = fleet pull trong ≤1h).

## Quickstart

```bash
curl -fsSL https://raw.githubusercontent.com/tkiet2105/bestphone-pppoe/main/deploy/install.sh | sudo bash
```

Sau khi cài, mở `http://<host>/`, đăng nhập bằng token in ở cuối install (cũng nằm trong `/etc/default/bestphone-pppoe`).

## Trạng thái

📋 **Design phase** — code chưa được implement. Xem [PLAN.md](PLAN.md) để biết toàn bộ kiến trúc, schema, API, install flow, và critical files cần tạo.

Repository này là **design doc** cho Claude session / engineer kế tiếp đọc → triển khai source theo cấu trúc trong PLAN.md.

## License

Private. © tkiet2105.
