---
title: "SSG 静态站点开发指南"
date: "2026-08-15"
author: "Aluka 团队"
summary: "如何使用 TypeScript + Markdown 高效构建内容站点。"
---

# SSG 静态站点开发指南

在 Aluka 中开发 SSG 站点非常轻量直观：

```typescript
import fs from 'node:fs';
import markdown from 'aluka:markdown';

const mdContent = fs.readFileSync('doc.md', 'utf-8');
const { data, content } = markdown.parseFrontmatter(mdContent);
const html = markdown.render(content);
```

无需安装巨大的 Node 工具链，开箱即用。
