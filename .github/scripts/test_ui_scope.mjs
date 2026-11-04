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

test("transparent layout, display copy and decorative icons retain functional syntax", () => {
  const before = 'import { Play } from "lucide-react"; const view = <div className="old"><button onClick={save} disabled={busy}><Play size={16} />{t("save")}</button></div>;';
  const after = 'import { Check } from "lucide-react"; const view = <section className="new"><div><button onClick={save} disabled={busy}><Check size={18} />{t("confirm")}</button></div></section>;';
  assert.ok(presentationOnly(before, after));
  for (const changed of [after.replace("onClick={save}", "onClick={remove}"), after.replace("disabled={busy}", "disabled={false}"),
    after.replace("<div>", '<div onClick={remove}>'), after.replace("<section", "<form").replace("</section>", "</form>"),
    after.replace("<Check size={18}", "<Check onClick={remove} size={18}"), after.replace('{t("confirm")}', '{t("confirm", { count: send() })}')]) {
    assert.equal(presentationOnly(before, changed), false, changed);
  }
  for (const attribute of ["value", "defaultValue", "href", "name"]) {
    assert.equal(presentationOnly(`const x = <input ${attribute}={t("a")} />;`, `const x = <input ${attribute}={t("b")} />;`), false);
  }
  assert.equal(presentationOnly('const x = useState(t("a"));', 'const x = useState(t("b"));'), false);
  assert.equal(presentationOnly('const x = <div id="anchor"><button /></div>;', 'const x = <div><button /></div>;'), false);
});
