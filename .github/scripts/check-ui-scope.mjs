// Actions-only: reject UI fast paths unless executable TSX syntax is unchanged.
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import { appendFileSync, readFileSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const require = createRequire(new URL("../../web/package.json", import.meta.url));
const { parseSync } = await import(pathToFileURL(require.resolve("rolldown/utils")).href);

export function presentationSignature(source) {
  // Reuse Vite's locked parser: TypeScript 7 no longer exposes the old JS compiler API.
  const result = parseSync("ui.tsx", source);
  if (result.errors.length) throw new Error("Invalid TSX");
  const icons = new Set(result.program.body.filter(node => node.type === "ImportDeclaration" && node.source.value === "lucide-react")
    .flatMap(node => node.specifiers.filter(item => item.type === "ImportSpecifier").map(item => item.local.name)));
  const literal = node => node?.type === "Literal" && ["string", "number"].includes(typeof node.value)
    || node?.type === "UnaryExpression" && node.operator === "-" && typeof node.argument?.value === "number";
  const translation = node => node?.type === "CallExpression" && node.callee.type === "Identifier" && node.callee.name === "t"
    && node.arguments[0]?.type === "Literal" && typeof node.arguments[0].value === "string";
  const style = node => node?.type === "ObjectExpression" && node.properties.every(property => property.type === "Property"
    && property.kind === "init" && !property.method && !property.computed && literal(property.value));
  function children(nodes) {
    return nodes.flatMap(node => {
      const value = normalize(node);
      return value === null ? [] : value?.type === "PresentationLayout" ? value.children : [value];
    });
  }
  function normalize(node) {
    if (Array.isArray(node)) return node.map(normalize);
    if (!node || typeof node !== "object") return node;
    if (node.type === "JSXText" || node.type === "JSXExpressionContainer" && node.expression.type === "JSXEmptyExpression") return null;
    if (node.type === "JSXExpressionContainer" && translation(node.expression)) {
      return { type: "DisplayTranslation", args: node.expression.arguments.slice(1).map(normalize) };
    }
    if (node.type === "ImportDeclaration" && node.source.value === "lucide-react") return { type: "IconImport" };
    if (node.type === "JSXFragment") return { type: "PresentationLayout", children: children(node.children) };
    if (node.type === "JSXElement") {
      const opening = node.openingElement;
      const name = opening.name.name;
      const decorative = icons.has(name) && opening.attributes.every(attr => attr.type === "JSXAttribute"
        && ["size", "strokeWidth", "className", "color", "aria-hidden"].includes(attr.name.name)
        && (attr.value?.type === "Literal" || attr.value?.type === "JSXExpressionContainer" && literal(attr.value.expression)));
      if (decorative && !node.children.length) return null;
      const layout = ["div", "span", "section", "article", "header", "footer"].includes(name)
        && opening.attributes.every(attr => attr.type === "JSXAttribute" && (attr.name.name === "className"
          && attr.value?.type === "Literal" || attr.name.name === "style" && style(attr.value?.expression)));
      if (layout) return { type: "PresentationLayout", children: children(node.children) };
      return { type: node.type, opening: normalize(opening), children: children(node.children) };
    }
    if (node.type === "JSXAttribute") {
      const name = node.name.name;
      const value = node.value;
      const staticText = value?.type === "Literal" && typeof value.value === "string"
        || value?.type === "JSXExpressionContainer" && value.expression?.type === "Literal" && typeof value.expression.value === "string";
      const staticStyle = name === "style" && value?.type === "JSXExpressionContainer" && style(value.expression);
      if (["className", "title", "aria-label", "placeholder"].includes(name) && staticText || staticStyle) {
        return { type: node.type, name, value: "presentation" };
      }
      // Only display attributes may change translation keys. Values, defaults,
      // validation, and callbacks keep their complete executable expressions.
      if (["title", "aria-label", "placeholder"].includes(name) && translation(value?.expression)) {
        return { type: node.type, name, value: { type: "DisplayTranslation", args: value.expression.arguments.slice(1).map(normalize) } };
      }
      return Object.fromEntries(Object.keys(node).sort().filter(key => !["start", "end", "loc", "range", "raw"].includes(key))
        .map(key => [key, key === "value" && value?.type === "JSXExpressionContainer"
          ? { type: value.type, expression: normalize(value.expression) } : normalize(node[key])]));
    }
    return Object.fromEntries(Object.keys(node).sort().filter(key => !["start", "end", "loc", "range", "raw"].includes(key))
      .map(key => [key, normalize(node[key])]));
  }
  return JSON.stringify(normalize(result.program));
}

export function presentationOnly(before, after) {
  return presentationSignature(before) === presentationSignature(after);
}

function main() {
  const plan = JSON.parse(readFileSync(".ci/ci-scope.json", "utf8"));
  const paths = execFileSync("git", ["diff", "--no-renames", "--name-only", "-z", plan.acceptance_base, "HEAD", "--"], { encoding: "utf8" }).split("\0").filter(Boolean);
  const uncertain = paths.filter(path => path.endsWith(".tsx") || path.endsWith(".svg")).filter(path => {
    // SVG can contain active content; keep it on full acceptance for now.
    if (path.endsWith(".svg")) return true;
    try {
      const before = execFileSync("git", ["show", `${plan.acceptance_base}:${path}`], { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
      return !presentationOnly(before, readFileSync(path, "utf8"));
    } catch { return true; }
  });
  const scope = uncertain.length ? "full" : "ui";
  const specs = new Set(["appearance.spec.ts", "workspace-layout.spec.ts"]);
  const global = paths.some(path => path.endsWith(".css") || path.includes("/locales/") || ["web/src/App.tsx", "web/src/i18n.tsx", "web/src/main.tsx"].includes(path) || path.includes("/ui/") || /\/(resource|page-heading|detail-panel|theme-selector)\.tsx$/.test(path));
  const affected = {
    "library": ["library-experience", "lyrics", "library-comparison", "maintenance", "maintenance-progress"], "lyrics": ["lyrics"],
    "repair": ["repair"], "maintenance": ["maintenance", "maintenance-progress"], "artwork-editor": ["maintenance"], "artifact-audition": ["artifact-audition", "maintenance"], "overview": ["cleanup-and-ui"],
  };
  if (global) for (const name of ["library-experience", "ui-refinement", "cleanup-and-ui", "bulk-jobs", "repair", "lyrics", "library-comparison", "session-recovery", "maintenance", "maintenance-progress"]) specs.add(`${name}.spec.ts`);
  else for (const [part, names] of Object.entries(affected)) if (paths.some(path => path.includes(part))) for (const name of names) specs.add(`${name}.spec.ts`);
  writeFileSync(".ci/ui-specs.txt", [...specs].join("\n") + "\n");
  // Existing representative cases retain desktop/phone, theme and keyboard checks.
  // Full acceptance still runs every case; presentation changes avoid replaying
  // unrelated authentication, retry and multi-page batch scenarios.
  const cases = ["translation catalogs", "default albums", "top-right controls", "aligned records", "phone pages retain",
    "album rows expose", "settings sections preserve", "sidebar collapse", "dashboard uses", "task details expand",
    "lyrics selection, background", "failed lyric parsing", "file comparison explains", "best-effort repair requires", "maintenance workbench", "schedule and notifications", "lyric editor previews", "output cover correction", "inline audition plays", "saved library views", "saved views reject", "maintenance progress and grouped recovery"];
  writeFileSync(".ci/ui-grep.txt", cases.join("|") + "\n");
  plan.scope = scope;
  if (uncertain.length) {
    plan.required = true;
    plan.reason = "frontend executable or uncertain changes may affect persisted settings";
  }
  plan.uncertain_ui_files = uncertain;
  plan.browser_specs = [...specs];
  writeFileSync(".ci/ci-scope.json", JSON.stringify(plan, null, 2) + "\n");
  writeFileSync(".ci/rollback-scope.json", JSON.stringify(plan, null, 2) + "\n");
  appendFileSync(process.env.GITHUB_OUTPUT, `scope=${scope}\nrollback_required=${plan.required}\n`);
  appendFileSync(process.env.GITHUB_STEP_SUMMARY, uncertain.length
    ? `\nTSX/static-content gate: **full acceptance**; executable or uncertain changes: ${uncertain.map(path => `\`${path}\``).join(", ")}.\n`
    : `\nTSX gate: **presentation only**. UI browser checks: ${[...specs].join(", ")}.\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main();
