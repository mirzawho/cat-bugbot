// Admin panel: plain JS + fetch against /admin/api.
const ADMIN_PANEL = (() => {
  const state = { reports: [], tab: "all", chatting: null };

  const $ = (sel, root = document) => root.querySelector(sel);
  const list = $("#list");
  const empty = $("#empty");
  const loading = $("#loading");
  const errorBox = $("#error");
  const cardTemplate = $("#card");

  const statusLabels = { new: "گزارش جدید", review: "در حال بررسی", resolved: "حل شده", rejected: "حذف شده" };

  function showError(msg) {
    errorBox.textContent = msg;
    errorBox.classList.remove("hidden");
  }

  function clearError() {
    errorBox.classList.add("hidden");
  }

  async function api(path, options = {}) {
    const res = await fetch(path, {
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", ...(options.headers || {}) },
      ...options,
    });
    if (res.status === 401) {
      location.href = "/admin/login";
      throw new Error("unauthorized");
    }
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body.error || `خطا (${res.status})`);
    return body;
  }

  function tabReports() {
    if (state.tab === "all") return state.reports;
    return state.reports.filter((r) => r.status === state.tab);
  }

  function renderTabs() {
    const sheet = document.getElementById("chat-sheet");
  if (sheet) {
    sheet.addEventListener("click", (e) => {
      if (e.target.matches("[data-close-sheet]")) closeChatSheet();
    });
    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape") closeChatSheet();
    });
  }

  document.querySelectorAll(".tab").forEach((btn) => {
      const tab = btn.dataset.tab;
      btn.classList.toggle("active", tab === state.tab);
    });
    const counts = { all: state.reports.length };
    for (const r of state.reports) counts[r.status] = (counts[r.status] || 0) + 1;
    document.querySelectorAll("[data-count]").forEach((el) => {
      const n = counts[el.dataset.count] || 0;
      el.textContent = n ? `(${n.toLocaleString("fa")})` : "";
    });
  }

  function mediaNode(item) {
    const wrap = document.createElement("div");
    wrap.className = "relative";
    const el = item.type === "video" ? document.createElement("video") : document.createElement("img");
    el.src = item.url;
    if (item.type === "video") {
      el.controls = true;
      el.preload = "metadata";
    } else {
      el.loading = "lazy";
      el.alt = "مدیای گزارش";
    }
    el.addEventListener("error", async () => {
      let text = "مدیا در دسترس نیست";
      if (item.type === "image") {
        // Show the server's explanation instead of a silent broken image.
        try {
          const res = await fetch(item.url, { credentials: "same-origin" });
          if (!res.ok) text = (await res.text()).trim() || text;
        } catch {
          /* keep the generic text */
        }
      }
      wrap.replaceWith(placeholder(text));
    });
    wrap.appendChild(el);
    if (item.type === "video") {
      const badge = document.createElement("span");
      badge.className = "absolute bottom-2 left-2 rounded-md bg-black/70 px-2 py-0.5 text-xs";
      badge.textContent = "🎬 ویدئو";
      wrap.appendChild(badge);
    }
    return wrap;
  }

  function placeholder(text) {
    const ph = document.createElement("div");
    ph.className = "placeholder";
    ph.textContent = text;
    return ph;
  }

  function actionButton(label, cls, onClick) {
    const b = document.createElement("button");
    b.className = `btn-full rounded-xl px-4 py-2.5 text-sm font-semibold ${cls}`;
    b.textContent = label;
    b.addEventListener("click", onClick);
    return b;
  }

  // showName prefers the reporter's live Telegram name, then the stored one.
  function showName(u) {
    if (u.display_name && u.display_name.trim()) return u.display_name;
    const full = [u.first_name, u.last_name].filter(Boolean).join(" ").trim();
    return full || u.username || "کاربر کت";
  }

  function renderCard(r) {
    const node = cardTemplate.content.firstElementChild.cloneNode(true);
    const media = $(".media", node);

    if (r.media.length) {
      r.media.forEach((item) => media.appendChild(mediaNode(item)));
    } else {
      media.appendChild(placeholder("بدون مدیا"));
    }

    const cat = $(".category", node);
    cat.textContent = `#${r.category}`;
    cat.href = "#";
    cat.addEventListener("click", (e) => {
      e.preventDefault();
      state.tab = "all";
      render();
    });

    const st = $(".status", node);
    st.textContent = statusLabels[r.status] || r.status;
    st.classList.add(`status-${r.status}`);

    const avatar = $(".avatar", node);
    if (r.user.photo_url) {
      avatar.src = r.user.photo_url;
    } else {
      avatar.replaceWith(makeInitials(showName(r.user)));
    }
    $(".name", node).textContent = showName(r.user);
    $(".handle", node).textContent = r.user.username ? `@${r.user.username}` : `#${r.user.id}`;
    $(".description", node).textContent = r.description;
    $(".date", node).textContent = new Date(r.created_at).toLocaleString("fa-IR", {
      dateStyle: "medium",
      timeStyle: "short",
    });

    const actions = $(".actions", node);
    const busy = (fn) => async () => {
      clearError();
      try {
        await fn();
      } catch (err) {
        showError(err.message);
      }
    };

    // Actions depend on the status:
    //   new    → بررسی گزارش / حذف گزارش / چت
    //   review → تایید گزارش (حل شد) / حذف گزارش / چت
    //   resolved or rejected → nothing left to act on, and chatting is closed.
    const chatButton = () =>
      actionButton("چت با گزارش‌دهنده", "bg-slate-700 text-white hover:bg-slate-600", busy(() => openChatSheet(r)));
    const rejectButton = () =>
      actionButton("حذف گزارش", "bg-rose-600/90 text-white hover:bg-rose-500", busy(() => setStatus(r.id, "rejected")));

    if (r.status === "new") {
      actions.append(
        actionButton("بررسی گزارش", "bg-sky-600 text-white hover:bg-sky-500", busy(() => setStatus(r.id, "review"))),
        rejectButton(),
        chatButton(),
      );
    } else if (r.status === "review") {
      actions.append(
        actionButton("تایید گزارش (حل شد)", "bg-emerald-600 text-white hover:bg-emerald-500", busy(() => setStatus(r.id, "resolved"))),
        rejectButton(),
        chatButton(),
      );
    }

    return node;
  }

  function makeInitials(name) {
    const span = document.createElement("span");
    span.className =
      "initials flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-indigo-600 text-sm font-bold text-white";
    span.textContent = (name || "کت").trim().charAt(0);
    return span;
  }

  function avatarNode(u, size = "h-9 w-9") {
    if (!u.photo_url) return makeInitials(showName(u));
    const img = document.createElement("img");
    img.className = `${size} shrink-0 rounded-full bg-slate-800 object-cover`;
    img.alt = "";
    img.src = u.photo_url;
    img.addEventListener("error", () => img.replaceWith(makeInitials(showName(u))));
    return img;
  }

  function chatBubble(m) {
    const b = document.createElement("div");
    b.className = `bubble ${m.sender}`;
    b.textContent = m.text;
    const t = document.createElement("time");
    t.textContent = new Date(m.created_at).toLocaleTimeString("fa-IR", { hour: "2-digit", minute: "2-digit" });
    b.appendChild(t);
    return b;
  }

  // openChatSheet shows the conversation with the reporter in a bottom sheet
  // (a centred dialog on desktop) instead of pushing the card around.
  function openChatSheet(r) {
    const sheet = $("#chat-sheet");
    const name = showName(r.user);
    const handle = r.user.username ? `@${r.user.username}` : `#${r.user.id}`;
    const header = $("#sheet-header");
    header.replaceChildren(avatarNode(r.user, "h-10 w-10"));
    const who = document.createElement("div");
    who.className = "min-w-0";
    who.innerHTML = '<p class="truncate font-semibold leading-tight"></p><p class="truncate text-xs text-slate-400" dir="ltr"></p>';
    who.children[0].textContent = name;
    who.children[1].textContent = handle;
    header.appendChild(who);
    const cat = document.createElement("span");
    cat.className = "mr-auto shrink-0 rounded-lg bg-indigo-500/15 px-2 py-1 text-xs font-semibold text-indigo-300";
    cat.textContent = `#${r.category}`;
    header.appendChild(cat);

    const box = $("#sheet-messages");
    box.replaceChildren(pageLoader("در حال بارگذاری گفت‌وگو…"));
    const form = $("#sheet-form");
    sheet.classList.remove("hidden");
    sheet.setAttribute("aria-hidden", "false");
    document.body.classList.add("sheet-open");
    requestAnimationFrame(() => sheet.classList.add("open"));
    setTimeout(() => form.querySelector("input").focus(), 250);

    const refresh = async () => {
      try {
        const { messages } = await api(`/admin/api/reports/${encodeURIComponent(r.id)}/messages`);
        if (!messages.length) {
          box.replaceChildren(emptyHint("هنوز پیامی ردوبدل نشده. اولین پیام را بفرست تا گفت‌وگو در تلگرام برای گزارش‌دهنده باز شود."));
          return;
        }
        box.replaceChildren(...messages.map(chatBubble));
        box.scrollTop = box.scrollHeight;
      } catch (err) {
        box.replaceChildren(emptyHint(err.message));
      }
    };
    refresh();

    form.onsubmit = async (e) => {
      e.preventDefault();
      const input = form.querySelector("input");
      const text = input.value.trim();
      const send = form.querySelector("button");
      if (!text || send.disabled) return;
      send.disabled = true;
      try {
        await api(`/admin/api/reports/${encodeURIComponent(r.id)}/messages`, {
          method: "POST",
          body: JSON.stringify({ text }),
        });
        input.value = "";
        await refresh();
      } catch (err) {
        showError(err.message);
      } finally {
        send.disabled = false;
        input.focus();
      }
    };
  }

  function closeChatSheet() {
    const sheet = $("#chat-sheet");
    if (!sheet || sheet.classList.contains("hidden")) return;
    sheet.classList.remove("open");
    sheet.setAttribute("aria-hidden", "true");
    document.body.classList.remove("sheet-open");
    setTimeout(() => sheet.classList.add("hidden"), 200);
  }

  function pageLoader(text) {
    const d = document.createElement("div");
    d.className = "flex items-center justify-center gap-2 py-8 text-sm text-slate-400";
    d.innerHTML = '<span class="h-4 w-4 animate-spin rounded-full border-2 border-slate-700 border-t-indigo-500"></span>';
    d.appendChild(document.createTextNode(text));
    return d;
  }

  function emptyHint(text) {
    const p = document.createElement("p");
    p.className = "py-8 text-center text-sm text-slate-400";
    p.textContent = text;
    return p;
  }

  async function setStatus(reportID, status) {
    await api(`/admin/api/reports/${encodeURIComponent(reportID)}/status`, {
      method: "POST",
      body: JSON.stringify({ status }),
    });
    await load();
  }

  function render() {
    const rows = tabReports();
    list.replaceChildren(...rows.map(renderCard));
    list.classList.toggle("hidden", rows.length === 0);
    empty.classList.toggle("hidden", rows.length !== 0);
    renderTabs();
  }

  async function load() {
    clearError();
    try {
      const { reports } = await api("/admin/api/reports");
      state.reports = reports;
      loading.classList.add("hidden");
      render();
    } catch (err) {
      loading.classList.add("hidden");
      showError(err.message);
    }
  }

  const sheet = document.getElementById("chat-sheet");
  if (sheet) {
    sheet.addEventListener("click", (e) => {
      if (e.target.matches("[data-close-sheet]")) closeChatSheet();
    });
    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape") closeChatSheet();
    });
  }

  document.querySelectorAll(".tab").forEach((btn) => {
    btn.addEventListener("click", () => {
      state.tab = btn.dataset.tab;
      render();
    });
  });
  $("#refresh").addEventListener("click", () => load());

  // Poll rarely so new reports show up while the tab is open.
  setInterval(() => {
    if (document.visibilityState === "visible") load();
  }, 30000);

  load();

  return { render, api, load, setStatus, openChatSheet, closeChatSheet, renderCard, showError };
})();
