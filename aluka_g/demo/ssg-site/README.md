# Aluka SSG 静态站点生成示例

展示基于纯 Go Aluka 运行时与内置 `aluka:markdown` 渲染管线的零依赖 SSG 静态站点开发。

## 运行 SSG 构建

```bash
# 1. 运行构建脚本生成静态站点到 dist/
aluka build.ts

# 2. 或者将构建器打包为免环境依赖的单文件独立工具
aluka build --compile --outfile ssg-builder build.ts
./ssg-builder
```
