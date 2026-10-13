type StackNode = { indent: number; target: Record<string, unknown> };

// Only keys are decoded. Values keep the existing minimal scalar/map grammar.
function parseMapKey(line: string): { key: string; separatorIndex: number } {
  const quote = line[0];
  let key: string;
  let separatorIndex: number;
  if (quote === '"' || quote === "'") {
    let end = -1;
    let singleQuoted = "";
    for (let i = 1; i < line.length; i++) {
      const char = line[i];
      if (quote === '"' && char === "\\") {
        i++; // JSON.parse below validates the complete escape sequence.
        continue;
      }
      if (char === quote) {
        if (quote === "'" && line[i + 1] === "'") {
          singleQuoted += "'";
          i++;
          continue;
        }
        end = i;
        break;
      }
      if (quote === "'") singleQuoted += char;
    }
    if (end < 0) throw new SyntaxError("Unterminated YAML map key");
    key = quote === '"' ? JSON.parse(line.slice(0, end + 1)) : singleQuoted;
    separatorIndex = end + 1;
    while (/\s/.test(line[separatorIndex] ?? "") && separatorIndex < line.length) {
      separatorIndex++;
    }
    if (line[separatorIndex] !== ":") {
      throw new SyntaxError("Expected colon after quoted YAML map key");
    }
  } else {
    separatorIndex = line.indexOf(":");
    if (separatorIndex <= 0) throw new SyntaxError("Malformed YAML map key");
    key = line.slice(0, separatorIndex).trim();
    if (/['"]/.test(key)) throw new SyntaxError("Quote in unquoted YAML map key");
  }
  if (!key) throw new SyntaxError("Empty YAML map key");
  // JSON permits lone UTF-16 surrogates; do not turn an ill-formed key into a
  // different pattern when it later crosses an encoding boundary.
  for (let i = 0; i < key.length; i++) {
    const code = key.charCodeAt(i);
    if (code < 0x20 || code === 0x7f) throw new SyntaxError("Control in YAML map key");
    if (code >= 0xd800 && code <= 0xdbff) {
      const next = key.charCodeAt(++i);
      if (!(next >= 0xdc00 && next <= 0xdfff)) throw new SyntaxError("Lone surrogate in YAML map key");
    } else if (code >= 0xdc00 && code <= 0xdfff) {
      throw new SyntaxError("Lone surrogate in YAML map key");
    }
  }
  return { key, separatorIndex };
}

function setMapValue(target: Record<string, unknown>, key: string, value: unknown): void {
  // Decoded keys remain own enumerable data properties, including __proto__.
  // Replacing an existing property retains JavaScript's original key ordering.
  Object.defineProperty(target, key, { value, enumerable: true, writable: true, configurable: true });
}

export function parseSimpleYamlMap(input: string): Record<string, unknown> {
  const root: Record<string, unknown> = {};
  const stack: StackNode[] = [{ indent: -1, target: root }];

  const lines = input.split(/\r?\n/);
  for (const rawLine of lines) {
    if (!rawLine.trim() || rawLine.trimStart().startsWith("#")) {
      continue;
    }

    const indent = rawLine.length - rawLine.trimStart().length;
    const line = rawLine.trim();
    const { key, separatorIndex } = parseMapKey(line);
    const rawValue = line.slice(separatorIndex + 1).trim();

    while (stack.length > 1 && indent <= stack[stack.length - 1].indent) {
      stack.pop();
    }

    const current = stack[stack.length - 1].target;

    if (!rawValue) {
      const child: Record<string, unknown> = {};
      setMapValue(current, key, child);
      stack.push({ indent, target: child });
      continue;
    }

    let scalar = rawValue;
    if (
      (scalar.startsWith('"') && scalar.endsWith('"')) ||
      (scalar.startsWith("'") && scalar.endsWith("'"))
    ) {
      scalar = scalar.slice(1, -1);
    }

    setMapValue(current, key, scalar);
  }

  return root;
}

export function extractFrontmatter(markdown: string): string {
  const normalized = markdown.replace(/\r\n/g, "\n");
  if (!normalized.startsWith("---\n")) {
    return "";
  }

  const end = normalized.indexOf("\n---", 4);
  if (end === -1) {
    return "";
  }

  return normalized.slice(4, end);
}
