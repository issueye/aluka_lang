// M8-6 diff：Event / EventTarget / CustomEvent / MessageEvent / MessageChannel /
// MessagePort / BroadcastChannel。
const r = {};

// Event 类
const e = new Event('click', { bubbles: true, cancelable: true });
r.eType = e.type;
r.eBubbles = e.bubbles;
r.eCancelable = e.cancelable;
r.eComposed = e.composed;
r.eIsInstance = e instanceof Event;
r.eCtor = new Event('x').constructor === Event;
r.eIsCancelableDefault = new Event('x').cancelable;
r.eDefaultPrevented = (() => {
  const ev = new Event('c', { cancelable: true });
  ev.preventDefault();
  return ev.defaultPrevented + ':' + (ev.defaultPrevented === true);
})();
r.eDispatchResult = (() => {
  const et = new EventTarget();
  const ev = new Event('c', { cancelable: true });
  et.addEventListener('c', (x) => x.preventDefault());
  return et.dispatchEvent(ev);
})();
r.eStopPropagation = (() => {
  const et = new EventTarget();
  let hits = 0;
  const f = () => hits++;
  et.addEventListener('x', f);
  et.addEventListener('x', f);
  et.dispatchEvent(new Event('x'));
  return hits;
})();

// EventTarget
r.etIsInstance = new EventTarget() instanceof EventTarget;
r.etOnce = (() => {
  const et = new EventTarget();
  let n = 0;
  et.addEventListener('go', () => n++, { once: true });
  et.dispatchEvent(new Event('go'));
  et.dispatchEvent(new Event('go'));
  return n;
})();
r.etRemove = (() => {
  const et = new EventTarget();
  let n = 0;
  const h = () => n++;
  et.addEventListener('go', h);
  et.removeEventListener('go', h);
  et.dispatchEvent(new Event('go'));
  return n;
})();
r.etHandleEvent = (() => {
  const et = new EventTarget();
  let n = 0;
  et.addEventListener('go', { handleEvent() { n++; } });
  et.dispatchEvent(new Event('go'));
  return n;
})();
r.etTarget = (() => {
  const et = new EventTarget();
  let t = null;
  et.addEventListener('go', (ev) => { t = ev.target === et; });
  et.dispatchEvent(new Event('go'));
  return t;
})();

// CustomEvent
r.ceDetail = new CustomEvent('d', { detail: { n: 7 } }).detail.n;
r.ceIsInstance = new CustomEvent('d') instanceof CustomEvent;
r.ceIsEvent = new CustomEvent('d') instanceof Event;

// MessageEvent
r.meType = typeof MessageEvent;
if (typeof MessageEvent === 'function') {
  const me = new MessageEvent('msg', { data: { n: 1 }, origin: 'https://x', lastEventId: 'id1', ports: [] });
  r.meType2 = me.type;
  r.meData = JSON.stringify(me.data);
  r.meOrigin = me.origin;
  r.meLastEventId = me.lastEventId;
  r.mePortsLen = me.ports.length;
  r.meIsEvent = me instanceof Event;
  r.meIsMessageEvent = me instanceof MessageEvent;
  r.meDefaultData = new MessageEvent('x').data;
}

// MessageChannel / MessagePort
r.mpType = typeof MessagePort;
const mc = new MessageChannel();
r.port1IsPort = mc.port1 instanceof MessagePort;
r.port2IsPort = mc.port2 instanceof MessagePort;
r.portPostMsg = typeof mc.port1.postMessage;
r.portRef = typeof mc.port1.ref;
r.portUnref = typeof mc.port1.unref;
r.portHasRef = typeof mc.port1.hasRef;
r.portStart = typeof mc.port1.start;
r.portClose = typeof mc.port1.close;

const got = [];
mc.port2.onmessage = (ev) => {
  got.push('p2:' + ev.data + ':' + (ev instanceof MessageEvent) + ':' + (ev.constructor === MessageEvent));
};
mc.port1.postMessage('hi');
mc.port2.postMessage('back');

// BroadcastChannel（同频道投递）
const gotBc = [];
const bc1 = new BroadcastChannel('m8chan');
const bc2 = new BroadcastChannel('m8chan');
bc2.onmessage = (ev) => {
  gotBc.push('bc:' + ev.data + ':' + (ev instanceof MessageEvent) + ':' + (ev.target === bc2));
};
bc1.postMessage('broadcast');
r.bcName = bc1.name;
r.bcCloseType = typeof bc1.close;

setTimeout(() => {
  r.msgs = got.join('|');
  r.bcMsgs = gotBc.join('|');
  bc1.close();
  bc2.close();
  mc.port1.close();
  mc.port2.close();
  const sorted = {};
  Object.keys(r).sort().forEach((k) => { sorted[k] = r[k]; });
  console.log(JSON.stringify(sorted));
}, 60);
