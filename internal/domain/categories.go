package domain

var Categories = []string{
	"چت‌های خصوصی",
	"گروه‌ها",
	"کانال‌ها",
	"تماس صوتی",
	"تماس تصویری",
	"پیام‌های صوتی و ویدئویی",
	"ارسال و دریافت فایل",
	"اعلان‌ها",
	"جست‌وجو",
	"ورود به حساب و کد تأیید",
	"رابط کاربری و نمایش",
	"عملکرد و سرعت",
	"حریم خصوصی و امنیت",
	"سایر موارد",
}

func CategoryByKey(key string) (string, bool) {
	for _, c := range Categories {
		if CategoryKey(c) == key {
			return c, true
		}
	}
	return "", false
}

func CategoryKey(name string) string {
	for i, c := range Categories {
		if c == name {
			return keyOf(i)
		}
	}
	return ""
}

func keyOf(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('a' + i - 10))
}
