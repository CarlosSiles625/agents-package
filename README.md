# agent-sync

Sincroniza las instrucciones globales de agentes de OpenCode (`AGENTS.md` y
`agents/**/*.md`) desde un repositorio git hacia un directorio destino, de
forma segura y recuperable.

## Principios

- **Alcance explícito y versionado.** Solo se gestionan los archivos que
  coinciden con el manifiesto incorporado: el `AGENTS.md` de la raíz y los
  Markdown bajo `agents/`. Todo lo demás (skills, `opencode.json`, MCP,
  credenciales, ...) queda **fuera** y nunca se toca.
- **Nunca sobrescribe cambios ajenos.** Antes de escribir, compara el estado
  base (B), el destino local (L) y el commit remoto (R). Si L y R divergen del
  baseline, la ejecución se **aborta por completo** sin escribir nada. El
  baseline refleja siempre el commit remoto aceptado (incluidas las ausencias),
  nunca la copia local: un cambio local conservado sigue siendo un conflicto si
  el remoto cambia después.
- **Transacción con journal y copias de seguridad.** Antes de modificar el
  destino se guarda un journal y copias en el directorio de estado. Si algo
  falla, `recover` revierte lo aplicado y jamás pisa cambios hechos después.
- **Caché bare e inmutable.** El repositorio se clona como *bare* dentro del
  directorio de estado y se lee desde un commit inmutable, nunca desde un árbol
  de trabajo mutable. Solo se hace `fetch`; **nunca `push`**.
- **Rutas portables y sin enlaces.** Se rechazan rutas absolutas, traversal,
  letras de unidad, caracteres no portables y colisiones por mayúsculas. Cada
  componente gestionado se valida antes de leer, escribir, borrar, hacer copia
  o recuperar: si un componente es un symlink o una junction/reparse point, la
  operación se rechaza y nunca se sale del destino.

## Comandos

```
agent-sync install [flags]   descarga y aplica (apto para primera instalación)
agent-sync pull    [flags]   descarga y aplica
agent-sync status  [flags]   muestra el estado y el plan pendiente (sin fetch)
agent-sync recover [flags]   revierte una transacción pendiente
```

### Flags

| Flag | Descripción |
| --- | --- |
| `--repo <ruta\|url>` | repositorio git (requerido salvo que ya esté registrado) |
| `--branch <nombre>` | rama git (por defecto `main`) |
| `--target <dir>` | directorio destino |
| `--state <dir>` | directorio de estado |
| `--dry-run` | solo previsualiza; nunca escribe destino ni estado |
| `--yes` | asume «sí» para cambios (no para borrados) |
| `--allow-delete` | permite borrados |
| `--replace <ruta>` | adopta un archivo no gestionado que difiere (repetible) |

## Flujo típico

```sh
# Primera instalación
agent-sync install --repo https://host/agentes.git --branch main --target ~/.config/opencode --yes

# Actualizar
agent-sync pull --yes

# Previsualizar sin escribir
agent-sync pull --dry-run

# Recuperar tras una interrupción
agent-sync recover
```

Si un archivo local no gestionado difiere del remoto, la ejecución se bloquea y
hay que confirmarlo explícitamente con `--replace <ruta>` (se guarda copia de
seguridad antes de sobrescribir). Los borrados remotos requieren
`--allow-delete`.

## Compilación

```sh
go build ./...
go test ./...
```

Artefactos multiplataforma en `dist/` (Windows y Linux, `amd64`):

```sh
go build -o dist/agent-sync-windows-amd64.exe ./cmd/agent-sync
GOOS=linux GOARCH=amd64 go build -o dist/agent-sync-linux-amd64 ./cmd/agent-sync
```
