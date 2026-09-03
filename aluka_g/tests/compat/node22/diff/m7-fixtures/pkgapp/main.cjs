const r = require;
const results = {};
results.requireCond = r('condpkg').kind;
results.feature = r('condpkg/feature').kind;
results.wild = r('condpkg/wild/util').kind;
results.sub = r('condpkg/sub').kind;
results.alias = r('#alias').kind;
results.importsWild = r('#util/helper').kind;
import('condpkg').then((ns) => {
  results.importCond = (ns && ns.default ? ns.default.kind : (ns && ns.kind));
  process.stdout.write('RESULT ' + JSON.stringify(results));
}).catch((e) => {
  results.importCond = 'ERR:' + (e && e.message);
  process.stdout.write('RESULT ' + JSON.stringify(results));
});
