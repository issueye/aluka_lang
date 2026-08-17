// 项目打包配置钩子：由 Aluka 内置发现脚本加载（不写死 vite/vue 文件名）。
// 也可用 vite.config.js / vue.config.js：去掉本文件后由默认脚本按对象形态识别。
// CLI 显式 flag 始终优先于本文件。
function demoPlugin() {
  return {
    name: 'aluka-demo-meta',
    transformIndexHtml(html) {
      if (html.includes('name="aluka-plugin"')) {
        return html;
      }
      return html.replace(
        '</head>',
        '  <meta name="aluka-plugin" content="demo">\n</head>',
      );
    },
    generateBundle(filesJSON) {
      return {
        'plugin-manifest.json': JSON.stringify({
          plugin: 'aluka-demo-meta',
          files: JSON.parse(filesJSON),
        }),
      };
    },
  };
}

export default {
  // './' 便于直接打开 dist/index.html；部署到站点根可改成 '/'
  base: './',
  outDir: 'dist',
  assetsDir: 'assets',
  minify: true,
  vueCompiler: 'official',
  alias: {
    '@': './src',
  },
  define: {
    __APP_BUILD__: JSON.stringify('aluka-web'),
  },
  plugins: [demoPlugin()],
};
