// demo/ssg-site/build.ts
// Aluka SSG 静态站点生成构建管线

import fs from 'node:fs';
import path from 'node:path';
import markdown from 'aluka:markdown';
import { layout } from './templates/layout.ts';

const rootDir = process.cwd();
const postsDir = path.join(rootDir, 'content', 'posts');
const outDir = path.join(rootDir, 'dist');

if (!fs.existsSync(outDir)) {
    fs.mkdirSync(outDir, { recursive: true });
}

console.log("[Aluka SSG] 开始静态站点生成...");

const postFiles = fs.readdirSync(postsDir).filter(f => f.endsWith('.md'));
const posts: Array<{ slug: string; title: string; date: string; summary: string }> = [];

for (const file of postFiles) {
    const filePath = path.join(postsDir, file);
    const mdRaw = fs.readFileSync(filePath, 'utf-8');
    const { data, content } = markdown.parseFrontmatter(mdRaw);
    const htmlBody = markdown.render(content);

    const title = data.title || file.replace('.md', '');
    const date = data.date || '';
    const author = data.author || '';
    const summary = data.summary || '';
    const slug = file.replace('.md', '.html');

    posts.push({ slug, title, date, summary });

    const fullHTML = layout({
        title,
        content: htmlBody,
        date,
        author
    });

    fs.writeFileSync(path.join(outDir, slug), fullHTML, 'utf-8');
    console.log(`  ✓ 生成页面: dist/${slug}`);
}

// 生成首页 index.html
let indexBody = `<h1>Aluka 文档与博客</h1><p>纯 TypeScript 与内置 Markdown 渲染构建的内容站点：</p><ul>`;
for (const p of posts) {
    indexBody += `<li><a href="${p.slug}"><strong>${p.title}</strong></a> - <span>${p.date}</span><p>${p.summary}</p></li>`;
}
indexBody += `</ul>`;

const indexHTML = layout({
    title: "首页",
    content: indexBody
});

fs.writeFileSync(path.join(outDir, 'index.html'), indexHTML, 'utf-8');
console.log("  ✓ 生成首页: dist/index.html");
console.log(`[Aluka SSG] 构建完成！共生成 ${posts.length + 1} 个静态页面。`);
