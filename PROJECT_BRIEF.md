# PROJECT_BRIEF — BugBot

## هدف
ربات کت فارسی برای ثبت گزارش باگ‌های خود کت. کاربر با یک مکالمه مرحله‌به‌مرحله مدیا (تصویر/ویدئو) ارسال می‌کند، دسته‌بندی را انتخاب و تأیید می‌کند، توضیح اختیاری می‌نویسد و گزارش را نهایی می‌کند.

## محدوده نسخه اول
**در محدوده:**
- جریان کامل ثبت گزارش: خوشامد → مدیا → تأیید مدیا → دسته‌بندی → تأیید دسته‌بندی → توضیحات اختیاری → خلاصه → ثبت
- `/cancel` و دکمه «لغو گزارش» در همه مراحل
- `/start` با گزارش نیمه‌تمام: انتخاب «ادامه گزارش» یا «لغو و شروع مجدد»
- ذخیره پایدار وضعیت مکالمه و گزارش‌ها در SQLite (قابل بازیابی پس از Restart)
- جلوگیری از ثبت تکراری و رد Callbackهای قدیمی نامرتبط با مرحله فعلی

**خارج از محدوده (نسخه‌های بعدی):**
- پنل مدیریت، ارسال گزارش به ادمین، تغییر وضعیت، آمار، جست‌وجو
- مدیریت دسته‌بندی از داخل ربات
- دانلود و نگهداری فایل مدیا (فقط `file_id` / `file_unique_id` ذخیره می‌شود)

## پشته فنی
- **زبان:** Go 1.25
- **Telegram API:** پیاده‌سازی سبک روی `net/http` با Long Polling (بدون وابستگی بیرونی)
- **دیتابیس:** SQLite خالص Go (`modernc.org/sqlite`، بدون CGO) با مهاجرت‌های نسخه‌بندی‌شده
- **استقرار:** Docker (multi-stage، غیر Root)، docker-compose، Kubernetes (تک Replica + PVC)
- **CI/CD:** GitHub Actions → تست/Type Check → Build → GHCR → استقرار اختیاری K8s

## معماری

```
cmd/bot            نقطه ورود، polling loop، HTTP health server، shutdown
internal/telegram  کلاینت Telegram API و interface `API` (برای تست)
internal/bot       Handler مکالمه، متن‌ها، کیبوردها، Poller
internal/domain    مدل‌ها، وضعیت‌ها، دسته‌بندی‌ها
internal/store     SQLite: مهاجرت‌ها، DraftFor/SaveDraft/DeleteDraft/InsertReport
internal/config    خواندن متغیرهای محیطی
```

منطق مکالمه از Telegram API جدا است: `Handler` فقط به interface `telegram.API` و `store.Store` وابسته است؛ در تست‌ها با Fake API و دیتابیس موقت SQLite اجرا می‌شود.

## وضعیت‌های مکالمه
`awaiting_media` → `confirming_media` → `selecting_category` → `confirming_category` → `awaiting_description` → `confirming_submission`

هر کاربر حداکثر یک پیش‌نویس فعال دارد (`drafts.user_id` کلید اصلی). ثبت نهایی در یک تراکنش، پیش‌نویس را حذف و گزارش را درج می‌کند؛ بنابراین تأیید دوباره یا Callback تکراری گزارش دوم نمی‌سازد.

## مدل داده
- `users`: شناسه عددی، username، نام
- `drafts`: وضعیت، مدیا (JSON)، دسته‌بندی، توضیحات
- `reports`: شناسه یکتا (`BUG-<hex>`)، کاربر، مدیا (JSON)، دسته‌بندی، توضیحات، فیلد `status` پیش‌فرض `new` برای توسعه آینده، زمان ایجاد/به‌روزرسانی
- `schema_migrations`: نسخه مهاجرت‌های اعمال‌شده

## پیکربندی
| متغیر | پیش‌فرض | توضیح |
|---|---|---|
| `BOT_TOKEN` | — (الزامی) | توکن ربات از BotFather |
| `DATABASE_PATH` | `./data/bugbot.db` | مسیر SQLite روی Volume پایدار |
| `HEALTH_ADDR` | `:8080` | آدرس سرور `/healthz` (فقط Probeها) |
| `TELEGRAM_API_BASE` | `https://api.telegram.org` | برای تست/پروکسی |

## تصمیم‌های مهم
- **Polling نه Webhook:** سرویس HTTP عمومی لازم نیست؛ پورت 8080 فقط داخلی/برای Probe است. دامنه `cr0.ir` به Ingress متصل نشده است.
- **تک Replica:** SQLite به Volume `ReadWriteOnce` نیاز دارد؛ Scale افقی نیازمند تغییر به دیتابیس شبکه‌ای و استراتژی اجرای تک‌نویسنده یا Webhook با حذف polling است.
- **بدون دانلود مدیا:** فقط شناسه‌های کت ذخیره می‌شود؛ دسترسی مجدد با `file_id` سمت ادمین/نسخه‌های بعدی انجام خواهد شد.
