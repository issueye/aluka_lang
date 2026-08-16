export interface PageProps {
    title: string;
    content: string;
    date?: string;
    author?: string;
}

export function layout({ title, content, date, author }: PageProps): string {
    return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>${title} - Aluka SSG</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
            max-width: 800px;
            margin: 0 auto;
            padding: 32px 16px;
            line-height: 1.6;
            color: #24292e;
            background: #fafbfc;
        }
        header {
            border-bottom: 1px solid #e1e4e8;
            padding-bottom: 16px;
            margin-bottom: 32px;
        }
        nav a {
            margin-right: 16px;
            color: #0366d6;
            text-decoration: none;
            font-weight: 500;
        }
        nav a:hover {
            text-decoration: underline;
        }
        .meta {
            color: #586069;
            font-size: 14px;
            margin-bottom: 24px;
        }
        pre {
            background: #f6f8fa;
            padding: 16px;
            border-radius: 6px;
            overflow-x: auto;
        }
        code {
            font-family: SFMono-Regular, Consolas, "Liberation Mono", Menlo, monospace;
            background: rgba(27, 31, 35, 0.05);
            padding: 2px 4px;
            border-radius: 3px;
        }
        pre code {
            background: none;
            padding: 0;
        }
        footer {
            margin-top: 48px;
            border-top: 1px solid #e1e4e8;
            padding-top: 16px;
            color: #586069;
            font-size: 13px;
            text-align: center;
        }
    </style>
</head>
<body>
    <header>
        <nav>
            <a href="index.html">🏠 首页</a>
        </nav>
    </header>
    <main>
        ${date || author ? `<div class="meta">${date ? `发布日期: ${date} ` : ''}${author ? `| 作者: ${author}` : ''}</div>` : ''}
        ${content}
    </main>
    <footer>
        <p>由 Aluka SSG 静态构建引擎自研生成</p>
    </footer>
</body>
</html>`;
}
