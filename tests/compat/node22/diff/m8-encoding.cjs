// M8-4 diff：TextEncoder / TextDecoder / atob / btoa。
const r = {};

const te = new TextEncoder();
r.teEncoding = te.encoding;
const bytes = te.encode('Aé😀');
r.teBytes = [bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], bytes[5]].join(',');
r.teLen = bytes.length;
r.teEmpty = te.encode('').length;

// encodeInto
const u8 = new Uint8Array(10);
const en = te.encodeInto('héllo', u8);
r.eiRead = en.read;
r.eiWritten = en.written;
r.eiBytes = Array.from(u8).join(',');

// 空间不足截断
const u8small = new Uint8Array(3);
const en2 = te.encodeInto('héllo', u8small);
r.eiSmallRead = en2.read;
r.eiSmallWritten = en2.written;

// TextDecoder
const td = new TextDecoder();
r.tdEncoding = td.encoding;
r.tdDecode = td.decode(te.encode('héllo'));
r.tdBOM = td.decode(new Uint8Array([0xEF, 0xBB, 0xBF, 0x61, 0x62]));
r.tdInvalid = td.decode(new Uint8Array([0x61, 0xFF, 0x62]));
r.tdArrayBuffer = td.decode(te.encode('AB').buffer.slice(0, 2));
r.tdUndefined = td.decode(undefined);
r.tdNoArg = td.decode();
r.tdUtf16 = new TextDecoder('utf-16le').decode(new Uint8Array([0x41, 0x00, 0x42, 0x00]));
r.tdLatin1 = new TextDecoder('latin1').decode(new Uint8Array([0xE9]));
r.tdBOMUtf16 = new TextDecoder('utf-16le').decode(new Uint8Array([0xFF, 0xFE, 0x43, 0x00]));
r.tdIgnoreBOM = new TextDecoder('utf-8', { ignoreBOM: true }).decode(new Uint8Array([0xEF, 0xBB, 0xBF, 0x61]));
r.tdFatalProp = new TextDecoder().fatal;
r.tdOptionsProp = new TextDecoder('utf-8', { ignoreBOM: true }).ignoreBOM;

// atob / btoa
r.atob1 = atob('aGVsbG8=');
r.atob2 = atob('aGVsbG8');
r.atobErr = (() => { try { atob('a'); return 'no-throw'; } catch (e) { return e.name + ':' + e.code; } })();
r.btoa1 = btoa('hello');
r.btoa2 = btoa('Aé');
r.btoaErr = (() => { try { btoa('h€llo'); return 'no-throw'; } catch (e) { return e.name; } })();

// instanceof
r.teIsInstance = new TextEncoder() instanceof TextEncoder;
r.tdIsInstance = new TextDecoder() instanceof TextDecoder;

const sorted = {};
Object.keys(r).sort().forEach((k) => { sorted[k] = r[k]; });
console.log(JSON.stringify(sorted));
