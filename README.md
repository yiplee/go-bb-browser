# go-bb-browser

Private experiment: a **Go** reimplementation inspired by [bb-browser](https://github.com/epiral/bb-browser) (CLI + local daemon controlling a real browser session). The daemon talks to **Google Chrome only** via **[chromedp](https://github.com/chromedp/chromedp)** (Chrome DevTools Protocol) — it **never launches Chrome**, only **attaches over CDP** to a browser you already started with remote debugging — **no Chrome extension**, **no other browsers**.

Implementation strategy and milestones are documented in:

- [`docs/IMPLEMENTATION_PLAN.md`](docs/IMPLEMENTATION_PLAN.md)

No application code yet — planning phase only.
