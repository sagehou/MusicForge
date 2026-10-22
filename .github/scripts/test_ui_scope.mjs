import { test } from "node:test";
import assert from "node:assert/strict";
import { presentationOnly } from "./check-ui-scope.mjs";

test("presentation gates preserve event, API, state, structure and persistence syntax", () => {
  const original = 'const [n, setN] = useState(1); const view = <button className="old" style={{gap: 2}} onClick={() => api("/save", n)}>Save</button>;';
  assert.ok(presentationOnly(original, original.replace('"old"', '"compact"').replace('gap: 2', 'gap: 4').replace('>Save<', '>保存<')));
  for (const changed of [original.replace('useState(1)', 'useState(2)'), original.replace('/save', '/delete'),
    original.replace('onClick', 'onMouseDown'), original.replace('api("/save", n)', 'localStorage.clear()'),
    original.replace('className="old"', 'className={sendRequest()}'), original.replace('gap: 2', 'gap: sendRequest()'),
    original.replace('gap: 2', '...externalStyle'), original.replace('gap: 2', 'get gap() { return sendRequest(); }'),
    original.replace('<button', '<input').replace('</button>', '</input>'),
    original.replace('>Save<', '>{sendRequest()}<'), original.replace('>Save<', ' disabled>Save<')]) {
    assert.equal(presentationOnly(original, changed), false, changed);
  }
  assert.equal(presentationOnly('const x = <A className="a" />;', 'const x = <B className="b" />;'), false);
  assert.equal(presentationOnly('const x = <div className={value} />;', 'const x = <div className={other} />;'), false);
  assert.throws(() => presentationOnly('const x = <div />;', 'const x = <div'));
});
