# BugBot — ربات گزارش باگ کت

ربات کت فارسی برای ثبت گزارش باگ‌های خود کت. کاربر تصویر یا ویدئو ارسال می‌کند، بخش کت را انتخاب می‌کند، توضیح اختیاری می‌نویسد و گزارش را تأیید و ثبت می‌کند.

- زبان: Go 1.25
- Telegram API: Long Polling (بدون سرویس HTTP عمومی)
- دیتابیس: SQLite با مهاجرت‌های نسخه‌بندی‌شده
- استقرار: Docker / docker-compose / Kubernetes

مستندات کامل محدوده و معماری: [PROJECT_BRIEF.md](PROJECT_BRIEF.md)

## جریان مکالمه

1. `/start` → پیام خوشامد
2. ارسال تصویر یا ویدئو → تأیید («بله، تأیید می‌کنم» / «خیر، ارسال مجدد»)
3. انتخاب دسته‌بندی (Inline Keyboard) → تأیید دسته‌بندی
4. توضیحات اختیاری → «رد شدن از این مرحله» یا ارسال متن
5. خلاصه گزارش → «بله، ثبت گزارش» / «خیر، لغو گزارش»

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

Workflow: [.github/workflows/deploy.yml](.github/workflows/deploy.yml)

1. **test:** `gofmt`، `go vet`، `go test -race`، `go build`
2. **build-and-push:** Image روی GHCR با Tagهای `sha-<full>`، `<short-sha>` و `latest` → `ghcr.io/<owner>/<repo>`
3. **check-deploy-config:** بررسی وجود Secret
4. **deploy:** فقط اگر Secret `KUBE_CONFIG` تنظیم شده باشد اجرا می‌شود؛ Image جدید در Manifest جایگزین و `kubectl apply` + `rollout status` انجام می‌شود. اگر Secret نباشد، فقط Build و Publish انجام می‌شود و Workflow شکست نمی‌خورد.

Secretهای GitHub Actions:

| Secret | کاربرد |
|---|---|
| `KUBE_CONFIG` | kubeconfig با دسترسی Namespace `bugbot`، Base64 |
| `GITHUB_TOKEN` | خودکار؛ برای Push به GHCR |

```bash
kubectl config view --minify --raw | base64
```

خروجی را در Settings → Secrets and variables → Actions با نام `KUBE_CONFIG` ثبت کنید.

### آدرس API تلگرام (`TELEGRAM_API_BASE`)

ربات فقط با یک **سرور Bot API** حرف می‌زند: درخواست‌ها به `<base>/bot<TOKEN>/getUpdates` و فایل‌ها از `<base>/file/bot<TOKEN>/...` خوانده می‌شوند. اگر می‌خواهید از دامنه خودتان عبور کنید، باید آن دامنه واقعاً یک پروکسی/آینه همین API باشد:

```bash
# nginx روی cr0.ir: /bot... و /file/bot... → api.telegram.org
location ~ ^/(bot|file/bot) { proxy_pass https://api.telegram.org; }
```

اگر `cr0.ir` را فقط برای پنل مدیریت تنظیم کرده‌اید، `TELEGRAM_API_BASE` را خالی بگذارید تا مستقیم به `https://api.telegram.org` وصل شود. آدرس لاگ‌شده در خط اول اجرا (`telegram api: ...`) نشان می‌دهد ربات واقعاً به کجا می‌رود.

### آرشیو مدیا و `REPORT_CHAT_ID`

تلگرام `file_id` هر فایل را فقط برای همان رباتی می‌پذیرد که آن را دریافت کرده است؛ بنابراین اگر توکن عوض شود یا دیتابیس قدیمی باشد، `getFile` با `FILE_ID_INVALID` شکست می‌خورد. برای اینکه این مشکل دیگر پیش نیاید:

1. یک گروه/کانال خصوصی بسازید و ربات را ادمین کنید.
2. شناسه‌اش را (مثلاً `-1001234567890`) در `REPORT_CHAT_ID` بگذارید و ربات را دوباره اجرا کنید.

از این پس هر مدیای گزارش در همان لحظه به این گروه آرشیو می‌شود و `file_id` تازه (متعلق به ربات فعلی) ذخیره می‌شود؛ پنل هم همان را می‌خواند. اگر این متغیر خالی باشد، همان `file_id` ورودی ذخیره می‌شود.

## پنل داخل تلگرام (Mini App)

پنل یک [Telegram Mini App](https://core.telegram.org/bots/webapps) است و داخل خود تلگرام باز می‌شود (بدون مرورگر). راه‌اندازی، یک‌بار:

1. دامنه در `.env`: `SERVICE_URL=https://cr0.ir` (همان دامنه‌ای که پنل روی آن سرو می‌شود؛ باید **HTTPS** باشد).
2. در `@BotFather` → `/newapp` یک Mini App بسازید و آدرس `https://cr0.ir/admin/` را بدهید.
3. پنل را روی همان دامنه سرو کنید (در کلاستر با Ingress موجود در `k8s.yaml`، یا معادل آن در سطح سرور).
4. ربات را اجرا کنید. مدیرِ داخل `ADMINS` با `/start` یک کیبورد ثابت پایین صفحه می‌گیرد که دکمه «📊 پنل مدیریت» روی آن است؛ همان دکمه پنل را داخل تلگرام باز می‌کند. «📝 ثبت گزارش باگ» هم جریان گزارش را شروع می‌کند.

**امنیت:** داخل Mini App صفحه `Telegram.WebApp.initData` را به `POST /admin/miniapp/session` می‌فرستد؛ سرور امضا را با توکن ربات (HMAC‑SHA256 با کلید `WebAppData`) بررسی می‌کند، عمر `auth_date` را حداکثر ۲۴ ساعت می‌پذیرد و فقط اگر `user.id` در `ADMINS` باشد سشن را صادر می‌کند. کاربر غیرمدیر چه دکمه داشته باشد چه مستقیم لینک را باز کند، پاسخ ۴۰۳ می‌گیرد. ورود با رمز در مرورگر هم باقی می‌ماند.

اگر `SERVICE_URL` با `https://` شروع نشود، تلگرام Mini App را باز نمی‌کند؛ در این حالت `/panel` دلیلش را می‌گوید.

## پنل مدیریت

پنل روی `http://<host>:8080/admin/` (یا `https://cr0.ir/admin/` در کلاستر) سرو می‌شود و فقط مدیران وارد می‌شوند:

| متغیر | توضیح |
|---|---|
| `ADMINS` | شناسه‌های عددی تلگرام مدیران، با کاما: `12345,67890` |
| `ADMIN_PANEL_PASSWORD` | رمز ورود پنل؛ اگر `ADMINS` تنظیم شود الزامی است |
| `SERVICE_URL` | آدرس عمومی سرویس (برای Secure Cookie پشت HTTPS)، مثلاً `https://cr0.ir` |
| `ADMIN_PANEL_IP_ALLOWLIST_ENFORCED` و `ADMIN_PANEL_ALLOWED_IPS` | اختیاری؛ محدودکردن پنل به IPهای مشخص (مثلاً هنگام باز کردن داخل Mini App تلگرام که کوکی سشن را نگه نمی‌دارد) |

قابلیت‌ها: تب‌های «همه / حل شده‌ها / حذف شده‌ها»، کارت هر گزارش شامل مدیا (از طریق خود سرور از Bot API پروکسی می‌شود؛ توکن به مرورگر نمی‌رود)، `#دسته‌بندی`، نام و شناسه گزارش‌دهنده، متن توضیحات و دکمه‌های:

- **بررسی گزارش** → وضعیت «در حال بررسی» (از این وضعیت، «تایید گزارش» نمایش داده می‌شود)
- **تایید گزارش** → وضعیت «حل شده» و ارسال پیام تأیید به کاربر
- **حذف گزارش** → وضعیت «حذف شده» و اطلاع به کاربر؛ در هر مرحله قبل از بسته شدن گزارش در دسترس است و پس از آن، گزارش نهایی و غیرقابل تغییر است
- **چت با گزارش‌دهنده** → پس از اولین پیام مدیر، ربات به کاربر اطلاع می‌دهد و پیام‌های بعدی کاربر مستقیم به همین گفت‌وگو می‌رود (`/cancel` پایان می‌دهد)

### هویت گزارش‌دهنده در گزارش

نام، نام خانوادگی و `username` هنگام ثبت گزارش از پیام خودِ کاربر ذخیره می‌شوند (در یک تراکنش با خود گزارش). پنل هم برای نمایش نام/آواتار به‌روز، از تلگرام (`getChat` و `getUserProfilePhotos`) می‌خواند و نتیجه را ۱۰ دقیقه کش می‌کند.

### دسترسی به API پنل

درخواست‌های API از یکی از این سه راه معتبر می‌شوند:

1. کوکی سشن امضاشده (ورود با رمز در مرورگر).
2. هدر `x-cat-user-id` همراه با `x-cat-init-data` (همان `Telegram.WebApp.initData`): امضای تلگرام با توکن ربات بررسی می‌شود و فقط باید با همان ID مطابقت داشته باشد.
3. هدر `x-cat-user-id` از مسیر edge خودِ استقرار، وقتی `ADMIN_PANEL_IP_ALLOWLIST_ENFORCED=true` است و IP در `ADMIN_PANEL_ALLOWED_IPS` باشد. **در این حالت edge باید هدرهای `X-Cat-User-Id`/`X-Cat-Init-Data` را از کلاینت حذف کند**؛ در غیر این صورت هر کسی می‌تواند خود را مدیر جا بزند.

هر سه مسیر بعد از احراز هویت، ID را با `ADMINS` چک می‌کنند. هدرِ تنها (بدون init data و بدون edge مورد اعتماد) همیشه ۴۰۱ می‌گیرد.

API پنل (`/admin/api/...`) پشت سشن امضاشده (HMAC) است؛ بدون ورود، پاسخ `401` می‌گیرد:

```
GET  /admin/api/reports[?status=new|resolved|rejected]
POST /admin/api/reports/{id}/status       {"status":"review|resolved|rejected"}
        # new→review، new→rejected، review→resolved، review→rejected؛ بعد از انتها 409
GET  /admin/api/reports/{id}/messages
POST /admin/api/reports/{id}/messages     {"text":"..."}
GET  /admin/api/reports/{id}/media/{n}    (پروکسی فایل تلگرام)
```

ساخت Secret در کلاستر:

```bash
kubectl -n bugbot create secret generic bugbot-secret --from-literal=BOT_TOKEN='<توکن>' --from-literal=ADMIN_PANEL_PASSWORD='<رمز>' --from-literal=ADMINS='<شناسه‌ها>'
```

## Make

```bash
make help          # فهرست دستورها
make check         # همان مراحل job تست CI (tidy، fmt، vet، test)
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

تست‌ها با Fake API و SQLite موقت اجرا می‌شوند و Telegram واقعی لازم نیست. پوشش شامل: تصویر و ویدئو، رد مدیای نامعتبر، تأیید/رد مدیا، انتخاب/تأیید/رد دسته‌بندی، توضیحات یا رد آن، ثبت نهایی، لغو در همه مراحل، ادامه پس از Restart، جلوگیری از ثبت تکراری، و Callbackهای قدیمی/نامعتبر.

## ساختار

```
cmd/bot             نقطه ورود
internal/bot        منطق مکالمه، متن‌ها، کیبوردها
internal/domain     مدل‌ها، وضعیت‌ها، دسته‌بندی‌ها
internal/store      SQLite و مهاجرت‌ها
internal/telegram   کلاینت Telegram API
```
