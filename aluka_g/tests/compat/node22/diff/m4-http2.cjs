// M4-7 diff：node:http2 —— 模块表面、constants、getDefaultSettings、
// Http2Server/Session 构造器。
const http2 = require('node:http2');
const c = http2.constants;
const results = {};

// 1. 模块表面。
results.surface = [
  typeof http2.createServer, typeof http2.createSecureServer,
  typeof http2.connect, typeof http2.getDefaultSettings,
  typeof http2.getPackedSettings, typeof http2.getUnpackedSettings,
].join(',');

// 2. 伪头常量 + nghttp2 错误码。
results.headers = [
  c.HTTP2_HEADER_METHOD, c.HTTP2_HEADER_PATH, c.HTTP2_HEADER_SCHEME,
  c.HTTP2_HEADER_AUTHORITY, c.HTTP2_HEADER_STATUS, c.HTTP2_HEADER_PROTOCOL,
].join(',');

results.errors = [
  c.NGHTTP2_NO_ERROR, c.NGHTTP2_PROTOCOL_ERROR, c.NGHTTP2_INTERNAL_ERROR,
  c.NGHTTP2_FLOW_CONTROL_ERROR, c.NGHTTP2_REFUSED_STREAM, c.NGHTTP2_CANCEL,
].join(',');

// 3. 默认设置（键序与 Node 一致）。
results.settings = JSON.stringify(http2.getDefaultSettings());

// 4. sensitiveHeaders 为 Symbol。
results.sensitive = typeof http2.sensitiveHeaders;

process.stdout.write(JSON.stringify(results));
