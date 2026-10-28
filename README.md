# BugBot — ربات گزارش باگ کت

ربات کت فارسی برای ثبت گزارش باگ‌های خود کت. کاربر تصویر یا ویدئو ارسال می‌کند، بخش کت را انتخاب می‌کند، توضیح اختیاری می‌نویسد و گزارش را تأیید و ثبت می‌کند.

- زبان: Go 1.25
- Telegram API: Long Polling (بدون سرویس HTTP عمومی)
- دیتابیس: SQLite با مهاجرت‌های نسخه‌بندی‌شده
- استقرار: Docker / docker-compose / Kubernetes

مستندات کامل محدوده و معماری: [PROJECT_BRIEF.md](PROJECT_BRIEF.md)

## جریان مکالمه

1. `/start` → پیام خوشامد
2. ارسال تصویر یا ویدئو (**اختیاری**) → تأیید («بله، تأیید می‌کنم» / «خیر، ارسال مجدد»)؛ یا «بدون تصویر ادامه بده»
3. انتخاب دسته‌بندی (Inline Keyboard) → تأیید دسته‌بندی
4. توضیحات اختیاری → «رد شدن از این مرحله» یا ارسال متن
5. خلاصه گزارش → «بله، ثبت گزارش» / «خیر، لغو گزارش»

مدیا اختیاری است: هم دکمه‌ی «بدون تصویر ادامه بده» و هم یک پیام متنی در مرحله اول، گزارش را بدون تصویر ادامه می‌دهد و در خلاصه «بدون تصویر» نشان داده می‌شود. متنی که در آن مرحله فرستاده شود به‌عنوان توضیحات نگه داشته می‌شود.

تجربه کاربری: هر بار که کاربر چیزی می‌فرستد (تصویر، ویدئو یا متن)، پیام راهنمای قبلی ربات **حذف** و پیام راهنمای جدید **زیر پیام کاربر** ارسال می‌شود؛ پس سؤال همیشه آخرین پیام چت است و روی عکس کاربر نمی‌چسبد. دکمه‌ها روی همان پیام راهنمای فعلی ویرایش می‌شوند و پیام جدید نمی‌سازند. شناسه پیام راهنما در دیتابیس ذخیره می‌شود، پس بعد از Restart ادامه روی همان پیام کار می‌کند.

دستورها: `/start` و `/report` (شروع یا ادامه/لغو گزارش نیمه‌تمام؛ در گروه‌ها با `/report@نام_ربات` هم پذیرفته می‌شود)، `/cancel` (لغو گزارش فعال).

## اجرای محلی

```bash
cp .env.example .env
# BOT_TOKEN، ADMINS و ADMIN_PANEL_PASSWORD را در .env قرار دهید

./bin/bugbot        # یا: make run
```

برنامه فایل `.env` را به‌طور خودکار می‌خواند (از پوشه فعلی، کنار باینری و ریشه پروژه). با `DOTENV_PATH=/path/to/.env` می‌توانید فایل دیگری بدهید. بعد از اجرا پنل مدیریت: `http://localhost:8080/admin/`

دیتابیس به‌صورت خودکار در `./data/bugbot.db` ساخته و مهاجرت می‌شود. بررسی سلامت: `curl http://127.0.0.1:8080/healthz`

## Docker

```bash
cp .env.example .env
# BOT_TOKEN را در .env قرار دهید

docker compose up -d --build
```

- دیتابیس روی Volume `bugbot-data` نگهداری می‌شود.
- کانتینر با کاربر غیر Root اجرا می‌شود، RootFS فقط‌خواندنی است و Healthcheck روی `/healthz` تعریف شده است.
- Image روی `scratch` ساخته می‌شود؛ Binary استاتیک و CA Certificateها به‌صورت Multi-stage کپی می‌شوند.

## Kubernetes

1. Secret را خارج از YAML بسازید (توکن هیچ‌وقت Commit نمی‌شود):

```bash
kubectl create namespace bugbot
kubectl -n bugbot create secret generic bugbot-secret --from-literal=BOT_TOKEN='<توکن ربات>'
```

2. مالک Image روی GHCR در `k8s.yaml` (`ghcr.io/mirzawho/bugbot`) از نام کاربری GitHub خودِ repo ساخته می‌شود؛ اگر نام کاربری دیگری دارید تغییرش دهید، سپس:

```bash
kubectl apply -f k8s.yaml
kubectl -n bugbot rollout status deployment/bugbot
```

نکته‌های استقرار:

- `replicas: 1` و استراتژی `Recreate` برای SQLite الزامی است؛ چند Replica هم‌زمان روی یک SQLite پشتیبانی نمی‌شود.
- دیتابیس روی PVC (`bugbot-data`، `ReadWriteOnce`) ذخیره می‌شود. `fsGroup: 10001` اجازه نوشتن به کاربر غیر Root می‌دهد.
- Probeها روی `/healthz` تنظیم شده‌اند. ربات از Polling استفاده می‌کند و Webhook نمی‌خواهد، اما پنل مدیریت `/admin/` است؛ به همین دلیل `k8s.yaml` یک Service و Ingress برای `cr0.ir` (مسیرهای `/admin` و `/healthz`) دارد.
- برای Scale افقی باید دیتابیس به Postgres/MySQL منتقل شود و به‌جای Polling از Webhook یا قفل تک‌نویسنده استفاده شود.

## CI/CD

Workflow: [.github/workflows/build.yml](.github/workflows/build.yml)

1. **test:** `go mod verify` و `go list -m all` (با `-mod=readonly`)، `gofmt`، `go vet`، `go test -race`، `go build`
2. **build-and-push:** ساخت Image به `ghcr.io/mirzawho/cat-bugbot` با Tagهای `sha-<full>`، `<short-sha>` و `latest`

استقرار روی Kubernetes در Workflow انجام نمی‌شود؛ Image منتشر می‌شود و دیپلوی دستی با `make k8s-apply` یا `kubectl apply -f k8s.yaml` است (بعد از تغییر Image در Manifest).
Secret `KUBE_CONFIG` دیگر لازم نیست.

## Make

```bash
make help          # فهرست دستورها
make check         # بررسی فرمت، vet و تست (مثل CI)
make test          # تست‌ها
make build         # باینری در bin/bugbot
make run           # اجرای محلی
make cover         # پوشش تست → coverage.out
make docker-up     # docker compose up -d --build
make k8s-apply     # اعمال k8s.yaml
```

## تست‌ها

```bash
go test ./...
go vet ./...
```

تست‌ها با Fake API و SQLite موقت اجرا می‌شوند و Telegram واقعی لازم نیست. پوشش شامل: تصویر و ویدئو، رد مدیای نامعتبر، تأیید/رد مدیا، انتخاب/تأیید/رد دسته‌بندی، توضیحات یا رد آن، ثبت نهایی، لغو در همه مراحل، ادامه پس از Restart، جلوگیری از ثبت تکراری، Callbackهای قدیمی/نامعتبر، جریان وضعیت گزارش‌ها در پنل، چت با گزارش‌دهنده و احراز هویت Mini App.

## پنل مدیریت

پنل روی `http://<host>:8080/admin/` (یا `https://cr0.ir/admin/` در کلاستر) سرو می‌شود و فقط مدیران وارد می‌شوند:

| متغیر | توضیح |
|---|---|
| `ADMINS` | شناسه‌های عددی تلگرام مدیران، با کاما: `12345,67890` |
| `ADMIN_PANEL_PASSWORD` | رمز ورود پنل؛ اگر `ADMINS` تنظیم شود الزامی است |
| `SERVICE_URL` | آدرس عمومی سرویس (برای Secure Cookie پشت HTTPS)، مثلاً `https://cr0.ir` |
| `ADMIN_PANEL_IP_ALLOWLIST_ENFORCED` و `ADMIN_PANEL_ALLOWED_IPS` | اختیاری؛ محدودکردن پنل به IPهای مشخص |

تب‌ها: همه (با شمارنده هر وضعیت شامل «در حال بررسی»)، حل شده‌ها و حذف شده‌ها. هر کارت مدیا (پروکسی‌شده از Bot API، بدون افشای توکن)، `#دسته‌بندی`، نام و شناسه گزارش‌دهنده و توضیحات را نشان می‌دهد. دکمه‌ها تمام‌عرض‌اند:

- **گزارش جدید:** بررسی گزارش · حذف گزارش · چت با گزارش‌دهنده
- **در حال بررسی:** تایید گزارش (حل شد) · حذف گزارش · چت با گزارش‌دهنده
- **حل شده / حذف شده:** بدون دکمه؛ گزارش نهایی است و چت بسته می‌شود

چت در یک bottom sheet باز می‌شود و پیام‌ها از سرور به تلگرام کاربر می‌رسند.

### هویت گزارش‌دهنده

نام، نام خانوادگی و `username` هنگام ثبت گزارش از پیام خودِ کاربر و در همان تراکنش ذخیره می‌شوند. پنل هم برای نام/آواتار به‌روز از `getChat` و `getUserProfilePhotos` می‌خواند (کش ۱۰ دقیقه‌ای).

### دسترسی به API پنل

1. کوکی سشن امضاشده (ورود با رمز در مرورگر).
2. هدر `x-cat-user-id` همراه با `x-cat-init-data` (امضای تلگرام با توکن ربات باید با همان ID بخواند).
3. هدر `x-cat-user-id` از edge خودتان، فقط با `ADMIN_PANEL_IP_ALLOWLIST_ENFORCED=true` و IP مجاز. در این حالت edge باید این هدرها را از کلاینت حذف کند.

هر سه مسیر ID را با `ADMINS` چک می‌کنند؛ هدرِ تنها همیشه ۴۰۱ می‌گیرد.

```
GET  /admin/api/reports[?status=new|review|resolved|rejected]
POST /admin/api/reports/{id}/status       {"status":"review|resolved|rejected"}
GET  /admin/api/reports/{id}/messages
POST /admin/api/reports/{id}/messages     {"text":"..."}   (فقط در حال بررسی)
GET  /admin/api/reports/{id}/media/{n}    (پروکسی فایل تلگرام)
```

## پنل داخل تلگرام (Mini App)

1. دامنه در `.env`: `SERVICE_URL=https://cr0.ir` (HTTPS لازم است).
2. در `@BotFather` → `/newapp` آدرس `https://cr0.ir/admin/` را ثبت کنید.
3. مدیرِ داخل `ADMINS` با `/start` کیبورد ثابت با دکمه «📊 پنل مدیریت» می‌گیرد که Mini App را داخل تلگرام باز می‌کند.

ورود از داخل Mini App با `Telegram.WebApp.initData` انجام می‌شود (امضا با توکن ربات، `auth_date` حداکثر ۲۴ ساعت و عضویت در `ADMINS`).

دستور `/panel` فقط در چت خودِ مدیران در منوی دستورات ثبت می‌شود؛ کاربران عادی نه آن را در منو می‌بینند، نه با فرستادن دستی‌اش پاسخی می‌گیرند.

### اعلان گزارش جدید

با ثبت هر گزارش، ربات به همه‌ی شناسه‌های `ADMINS` یک پیام می‌فرستد (گزارش‌دهنده، دسته‌بندی، توضیحات و شناسه گزارش) با دکمه‌ی «پنل مدیریت» که Mini App را داخل تلگرام باز می‌کند. ثبت‌کننده‌ی گزارش از این اعلان کنار گذاشته می‌شود و خطای ارسال به یک مدیر (مثلاً مدیری که هنوز ربات را استارت نکرده) ثبت گزارش را خراب نمی‌کند.

## API تلگرام و آرشیو مدیا

`TELEGRAM_API_BASE` باید ریشه یک سرور Bot API باشد؛ ربات به `<base>/bot<TOKEN>/getUpdates` و `<base>/file/bot<TOKEN>/...` درخواست می‌زند. اگر از دامنه خودتان عبور می‌دهید، آن دامنه باید همین مسیرها را به `api.telegram.org` پروکسی کند.

`REPORT_CHAT_ID` (اختیاری اما توصیه‌شده): شناسه یک گروه خصوصی که ربات در آن ادمین است. مدیای هر گزارش همان‌جا آرشیو می‌شود و `file_id` تازه ذخیره می‌شود؛ این کار خطای `FILE_ID_INVALID` را وقتی توکن عوض می‌شود از بین می‌برد.

## ساختار

```
cmd/bot             نقطه ورود
internal/bot        منطق مکالمه، متن‌ها، کیبوردها
internal/domain     مدل‌ها، وضعیت‌ها، دسته‌بندی‌ها
internal/store      SQLite و مهاجرت‌ها
internal/telegram   کلاینت Telegram API
```
