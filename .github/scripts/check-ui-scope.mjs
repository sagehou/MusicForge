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
  const literal = node => node?.type === "Literal" && ["string", "number"].includes(typeof node.value)
    || node?.type === "UnaryExpression" && node.operator === "-" && typeof node.argument?.value === "number";
  function normalize(node) {
    if (Array.isArray(node)) return node.map(normalize);
    if (!node || typeof node !== "object") return node;
    if (node.type === "JSXText") return { type: "JSXText" };
    if (node.type === "JSXAttribute") {
      const name = node.name.name;
      const value = node.value;
      const staticText = value?.type === "Literal" && typeof value.value === "string"
        || value?.type === "JSXExpressionContainer" && value.expression?.type === "Literal" && typeof value.expression.value === "string";
      const staticStyle = name === "style" && value?.type === "JSXExpressionContainer" && value.expression?.type === "ObjectExpression"
        && value.expression.properties.every(property => property.type === "Property" && property.kind === "init"
          && !property.method && !property.computed && literal(property.value));
      if (["className", "title", "aria-label", "placeholder"].includes(name) && staticText || staticStyle) {
        return { type: node.type, name, value: "presentation" };
      }
    }
    return Object.fromEntries(Object.keys(node).sort().filter(key => !["start", "end", "loc", "range", "raw"].includes(key))
      .map(key => [key, normalize(node[key])]));
  }
  return JSON.stringify(normalize({ program: result.program, comments: result.comments }));
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
    "library": ["library-experience", "lyrics", "library-comparison"], "lyrics": ["lyrics"],
    "repair": ["repair"], "overview": ["cleanup-and-ui"],
  };
  if (global) for (const name of ["library-experience", "ui-refinement", "cleanup-and-ui", "bulk-jobs", "repair", "lyrics", "library-comparison", "session-recovery"]) specs.add(`${name}.spec.ts`);
  else for (const [part, names] of Object.entries(affected)) if (paths.some(path => path.includes(part))) for (const name of names) specs.add(`${name}.spec.ts`);
  writeFileSync(".ci/ui-specs.txt", [...specs].join("\n") + "\n");
  plan.scope = scope;
  plan.uncertain_ui_files = uncertain;
  plan.browser_specs = [...specs];
  writeFileSync(".ci/ci-scope.json", JSON.stringify(plan, null, 2) + "\n");
  appendFileSync(process.env.GITHUB_OUTPUT, `scope=${scope}\n`);
  appendFileSync(process.env.GITHUB_STEP_SUMMARY, uncertain.length
    ? `\nTSX/static-content gate: **full acceptance**; executable or uncertain changes: ${uncertain.map(path => `\`${path}\``).join(", ")}.\n`
    : `\nTSX gate: **presentation only**. UI browser checks: ${[...specs].join(", ")}.\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main();
