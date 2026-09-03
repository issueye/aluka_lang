// M3-11 diff：constants 模块（node:constants / fs.constants / os.constants）。
const c = require('node:constants');
const fs = require('node:fs');
const os = require('node:os');
const r = {};

// node:constants 全量键（Windows 平台 242 个）。
r.constKeys = Object.keys(c).sort();
r.constCount = r.constKeys.length;
// 关键值抽查。
r.sigint = c.SIGINT;
r.sigkill = c.SIGKILL;
r.enoent = c.ENOENT;
r.eacces = c.EACCES;
r.epipe = c.EPIPE;
r.prioHigh = c.PRIORITY_HIGH;
r.prioLow = c.PRIORITY_LOW;
r.oCreate = c.O_CREAT;
r.oExcl = c.O_EXCL;
r.oTrunc = c.O_TRUNC;
r.sIfmt = c.S_IFMT;
r.sIfreg = c.S_IFREG;
r.copyfileExcl = c.COPYFILE_EXCL;
r.uvDirentDir = c.UV_DIRENT_DIR;
r.tls12 = c.TLS1_2_VERSION;
r.rsaPkcs1 = c.RSA_PKCS1_PADDING;
r.engineRsa = c.ENGINE_METHOD_RSA;
r.dhSafePrime = c.DH_CHECK_P_NOT_SAFE_PRIME;
r.sslNoTls12 = c.SSL_OP_NO_TLSv1_2;
r.opensslVerNum = c.OPENSSL_VERSION_NUMBER;
r.defaultCipherType = typeof c.defaultCoreCipherList;

// fs.constants 与 node:constants 交叉一致性。
r.fsConstKeys = Object.keys(fs.constants).sort();
r.fsConstCount = r.fsConstKeys.length;
r.fsSIfmt = fs.constants.S_IFMT;
r.fsSIfreg = fs.constants.S_IFREG;
r.fsSIfdir = fs.constants.S_IFDIR;
r.fsOCreate = fs.constants.O_CREAT;
r.fsORdonly = fs.constants.O_RDONLY;
r.fsOWronly = fs.constants.O_WRONLY;
r.fsORdwr = fs.constants.O_RDWR;
r.fsOAppend = fs.constants.O_APPEND;
r.fsFok = fs.constants.F_OK;
r.fsRok = fs.constants.R_OK;
r.fsWok = fs.constants.W_OK;
r.fsXok = fs.constants.X_OK;
r.fsCopyfileExcl = fs.constants.COPYFILE_EXCL;
r.fsCopyfileFiclonE = fs.constants.COPYFILE_FICLONE;
r.fsCopyfileFiclonEForce = fs.constants.COPYFILE_FICLONE_FORCE;
r.fsSIfchr = fs.constants.S_IFCHR;
r.fsSIfifo = fs.constants.S_IFIFO;
r.fsSIfLnk = fs.constants.S_IFLNK;
r.fsSIfBlk = fs.constants.S_IFBLK; // 可能 undefined（Windows）
r.fsSIfsock = fs.constants.S_IFSOCK; // 可能 undefined（Windows）
r.fsODirectory = fs.constants.O_DIRECTORY; // 可能 undefined（Windows）

// 常量与 fs/os 子对象一致：fs.constants.O_CREAT === node:constants.O_CREAT。
r.x = c.O_CREAT === fs.constants.O_CREAT;
r.y = c.S_IFMT === fs.constants.S_IFMT;
r.z = c.SIGINT === os.constants.signals.SIGINT;

// 兼容性：legacy 'constants' 裸名与 node:constants 同一键集。
try {
  const legacy = require('constants');
  r.legacyIdentical = JSON.stringify(Object.keys(legacy).sort()) === JSON.stringify(r.constKeys);
} catch (e) {
  r.legacyIdentical = 'err:' + e.code;
}

process.stdout.write(JSON.stringify(r));
