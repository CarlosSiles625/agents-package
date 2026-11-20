# agent-sync

Sincroniza las instrucciones globales de agentes de OpenCode (`AGENTS.md`,
`agents/**/*.md` y `skills/<id>/SKILL.md`) desde un repositorio git hacia un directorio destino, de
forma segura y recuperable.

## Principios

- **Alcance explícito y versionado.** Solo se gestionan los archivos que
  coinciden con el manifiesto incorporado: el `AGENTS.md` de la raíz, los
  Markdown bajo `agents/` y las skills de un nivel (`skills/<id>/SKILL.md`).
  Todo lo demás (otros archivos de skills, skills locales no gestionadas,
  `opencode.json`, MCP, credenciales, ...) queda **fuera** y nunca se toca.
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
agent-sync models  <sub>     elige el modelo de los agentes gestionados
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
| `--yes` | asume «sí» para cambios (no para borrados ni para el modelo) |
| `--allow-delete` | permite borrados |
| `--replace <ruta>` | adopta un archivo no gestionado que difiere (repetible) |
| `--add-to-path` | añade el directorio del ejecutable al PATH del usuario (solo `install`; sin pregunta) |
| `--shell bash\|zsh` | shell a configurar para el PATH del usuario (solo `install` en Linux; por defecto `$SHELL`) |
| `--catalog <url>` | catálogo de modelos (por defecto `https://models.dev/api.json`; solo `models`) |
| `--offline` | no verifica el modelo contra el catálogo; lo guarda como no verificado (`models set`) |

### Preferencias de modelo (`agent-sync models`)

```
agent-sync models list   [--catalog URL] [--provider ID]     lista el catálogo público
agent-sync models status [--target DIR] [--state DIR]        muestra las preferencias guardadas
agent-sync models set    <agente> <proveedor/modelo[#variante]> [--offline] [--dry-run]
agent-sync models inherit <agente>                           elimina el `model` del upstream
agent-sync models reset  <agente>                            restaura el `model` del upstream
agent-sync models select [<agente> [<proveedor/modelo>]]     selección interactiva
```

Una preferencia fija el campo `model` de un agente **sin tocar el resto del
archivo**. La edición se registra como un *recibo* reversible en el directorio de
estado (fuera del baseline crudo del upstream) y se vuelve a aplicar en cada
`pull`, de modo que la elección sobrevive a las actualizaciones del cuerpo. Solo
se pueden cambiar agentes gestionados (presentes en el baseline, bajo `agents/`);
las skills gestionadas nunca son objetivo de `models`, y `opencode.json`, MCP y
credenciales quedan fuera.

- `set <agente> <prov/modelo[#variante]>` inyecta o reemplaza el campo `model`.
- `inherit <agente>` elimina el `model` heredado del upstream.
- `reset <agente>` deshace la inyección y restaura el `model` del upstream.
- Si el campo `model` se editó a mano, el recibo ya no coincide y la operación
  **se rechaza** (fail-closed); una edición del cuerpo sí se respeta.
- Al borrar un agente del upstream se elimina el archivo y su entrada de
  baseline, pero la preferencia se conserva como *huérfana* por si el agente
  reaparece; los renombrados no se detectan. Por eso, al renombrar un agente
  (por ejemplo `commit-manager` → `git-manager`) la preferencia antigua queda
  huérfana y el nombre nuevo **no** la hereda: repite
  `agent-sync models set <nuevo-agente> <proveedor/modelo>` si quieres conservarla.

### Catálogo de modelos

`models list` y la verificación de `models set` leen el documento público
`https://models.dev/api.json` con un tiempo de espera acotado; no envían
credenciales, no detectan suscripciones ni cuentas y no escriben nada. Los
precios se publican en USD por millón de tokens (entrada, salida y, si existe,
caché); un precio ausente se muestra como `?` o «cost unknown», nunca como `0`.
Si el catálogo no está disponible, `models set` guarda la referencia como no
verificada (o puede forzarse con `--offline`), pero una referencia desconocida
con el catálogo accesible se rechaza.


## Instalación (scripts)

**Método principal.** En vez de compilar y resolver a mano el directorio del
binario, usa los scripts incluidos. Se ejecutan **desde la raíz de este
repositorio** (el checkout de `agent-sync`): comprueban los requisitos, compilan
con la versión local de Go, localizan el binario que instala `go install` (GOBIN
o el primer `GOPATH/bin`, sin que tú lo calcules) y lanzan el comando `install`
del CLI con la **ruta completa** del ejecutable. No descargan binarios ni
instalan Go/Git por ti y no usan `sudo` ni permisos de administrador.

Sin argumentos, el **origen por defecto es este checkout**: el CLI sincroniza los
agentes *commiteados* en su rama `main` (nunca el árbol de trabajo mutable) y los
instala en `~/.config/opencode/agents` (nivel raíz, sin el doble prefijo
`agents/agents`).

```powershell
# Windows (PowerShell): origen por defecto, este checkout.
.\install.ps1 -AddToPath   # instala y añade al PATH
.\install.ps1 -DryRun      # solo previsualiza
```

```sh
# Fedora / Linux (Bash, aunque tu shell sea Zsh): origen por defecto, este checkout.
bash ./install.sh --add-to-path  # instala y añade al PATH
bash ./install.sh --dry-run      # solo previsualiza
```

- **Requisitos.** Go y Git en el `PATH`. El script lee la versión requerida de
  `go.mod`, rechaza un Go local más antiguo o una versión inestable y nunca
  descarga otra toolchain (`GOTOOLCHAIN=local`).
- **Fuente de agentes.** `agents/**/*.md` de esta raíz es la **fuente
  autoritativa**: mejora o añade agentes aquí. El origen por defecto es la rama
  `main` **ya commiteada** de este checkout, nunca los archivos sin commitear; por
  eso, antes de una instalación real por defecto los agentes importados deben
  estar commiteados en `main` (si no, el preflight se bloquea con un mensaje
  claro y no se escribe nada). El script debe residir en la **raíz** del
  repositorio: si está dentro de un subdirectorio de otro repositorio git, el
  preflight también se bloquea (el CLI recibiría ese subdirectorio, que no es una
  fuente válida), salvo que pases `-Repo`/`--repo`.
- **Repositorio externo (opcional).** `-Repo`/`--repo` sobrescribe el origen por
  defecto y omite el preflight local; úsalo para sincronizar desde otro
  repositorio. No confundas este checkout (`agent-sync`) con el repositorio de
  agentes.

```powershell
# Origen externo (opcional): sobrescribe el checkout por defecto.
.\install.ps1 -Repo https://host/agentes.git -AddToPath
```

```sh
# Origen externo (opcional): sobrescribe el checkout por defecto.
bash ./install.sh --repo https://host/agentes.git --add-to-path
```

- **Windows y comillas.** Un valor de `-Repo` no puede contener una comilla doble
  (`"`), un salto de línea (CR/LF) ni un NUL: una ruta nativa de Windows tampoco
  puede, y PowerShell 5.1 podría reinterpretar la comilla y dividir el valor en
  flags extra del CLI. El script lo rechaza antes de compilar o llamar al CLI; si
  tu URL necesita una comilla, codifícala (por ejemplo, con percent-encoding).
- **Consentimiento.** `--add-to-path` autoriza **solo** el cambio de PATH; el
  script nunca reenvía `--yes`, de modo que el CLI conserva su propia confirmación
  interactiva. No automatices una respuesta afirmativa (`printf 'y' | ...`): pide
  al usuario su autorización explícita.
- **Destinos** (los crea el CLI de forma nativa): agentes en `~/.config/opencode`
  (`AGENTS.md` y `agents/**`, sin el doble prefijo `agents/agents`) y estado en
  `~/.local/state/agent-sync`.
- **`--dry-run`** solo previsualiza: no compila, no ejecuta el CLI, no crea
  archivos ni directorios y no escribe la toolchain; **no** valida el repositorio
  remoto. Con el origen por defecto sí ejecuta el preflight local (rama `main`
  commiteada y agentes elegibles) y reseña el checkout y el recuento; con
  `-Repo`/`--repo` no valida ningún remoto.
- **Shell.** En Windows no hay `--shell`; en Fedora/Linux el script fija
  `--shell zsh` (plataforma Fedora/Zsh).
- Si añadir al PATH falla, los agentes ya aplicados **se conservan** y el proceso
  termina con estado distinto de cero. Windows recarga el PATH de forma fiable al
  cerrar y volver a iniciar sesión (no se emite `WM_SETTINGCHANGE`); en Bash/Zsh,
  abre una terminal nueva.
- Si PowerShell bloquea el script por su política de ejecución, permite ese script
  local de confianza solo para este proceso con
  `pwsh -ExecutionPolicy Bypass -File .\install.ps1 ...`, sin cambiar la política
  global.


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

### Añadir el ejecutable al PATH (`install --add-to-path`)

`install` puede añadir el directorio del propio ejecutable al PATH del usuario en
el mismo comando; no hay un paso de instalación aparte. Para la primera
instalación usa los scripts de `## Instalación`; si ya tienes el CLI en el `PATH`:

```sh
agent-sync install --repo https://host/agentes.git --add-to-path             # Windows (sin --shell)
agent-sync install --repo https://host/agentes.git --add-to-path --shell zsh # Fedora / Linux
```

- Solo afecta al usuario actual: **no requiere administrador ni `sudo`**.
- `--add-to-path` es el consentimiento explícito que autoriza el cambio sin
  preguntar. `--yes` por sí solo **no** autoriza tocar el PATH.
- Nunca duplica una entrada ya presente ni copia el binario: usa el directorio
  estable del ejecutable (`os.Executable`, con symlinks resueltos). Si la ruta no
  se puede resolver se usa tal cual (limitación documentada).
- Si añadir al PATH falla, los archivos de agente ya aplicados **se conservan** y
  el comando termina con código 1.
- Copias de seguridad en `<state>/path-backups`: valor/bytes originales exactos,
  privadas y de solo usuario.
- Windows: escribe `HKCU\Environment\Path`. Un cierre y nuevo inicio de sesión es
  la forma fiable de recargarlo; una consola nueva puede heredar el valor
  obsoleto del proceso padre. Bash/Zsh: reabre la shell (o recarga el perfil)
  para tomar el valor; el cambio de PATH solo se aplica a procesos nuevos.

### Instalación manual (alternativa opcional)

El método principal son los scripts de `## Instalación`. Si prefieres hacer el
bootstrap a mano, con Go instalado (versión compatible con `go.mod`) y acceso a
Git para clonar el repositorio de agentes, compila e instala el CLI desde la raíz
de este repositorio. No confundas este repositorio (`agent-sync`) con el
repositorio de agentes: pide al usuario su URL y no la inventes. En Zsh:

```zsh
go install ./cmd/agent-sync

# Localiza el binario que acaba de instalar Go (GOBIN o el primer GOPATH/bin).
BIN_DIR="$(go env GOBIN)"
if [[ -z "$BIN_DIR" ]]; then
  BIN_DIR="$(go env GOPATH | cut -d: -f1)/bin"
fi
CLI="$BIN_DIR/agent-sync"

# Primero muestra el plan, sin aplicar cambios.
"$CLI" install --repo 'URL_DE_TUS_AGENTES' --add-to-path --shell zsh --dry-run
# Tras revisar el plan y contar con autorización, aplica la instalación.
"$CLI" install --repo 'URL_DE_TUS_AGENTES' --add-to-path --shell zsh
```

`--add-to-path` autoriza explícitamente modificar el PATH del usuario; no hace
falta `sudo`, y `--yes` por sí solo no da ese consentimiento. El comando añade al
PATH el directorio del ejecutable (no copia ni mueve el binario), actualizando el
perfil Zsh del usuario. Si `ZDOTDIR` está definido, el perfil se busca allí; de lo
contrario, en `HOME`. No recargues ni ejecutes automáticamente archivos de inicio
del usuario: abre una nueva sesión Zsh y comprueba `command -v agent-sync`. Si se
define `ZDOTDIR`, debe ser una ruta absoluta.

## Reparto de modelos por suscripción

Los agentes reparten la carga entre tres suscripciones según frecuencia de uso y
coste de error:

| Proveedor | Agentes | Criterio |
|---|---|---|
| OpenAI (`openai/gpt-6.1-sol`) | `orchestrator` (`#low`), `debugger` (`#medium`), `implementer-deep` (`#high`) | Trabajo continuo y de muchos pasos; el límite de Codex absorbe mejor el volumen. |
| Claude Opus 5.5 (`claude-agent/opus`) | `architect`, `debugger-deep` (`#medium`) | Baja frecuencia, error caro y resultado no verificable por tests. |
| Claude Sonnet 5.5 (`claude-agent/sonnet`) | `reviewer`, `security-reviewer`, `database-specialist` (`#medium`) | Juicio y revisión con un modelo de otra familia que el que implementó. |
| OpenCode Go | el resto | Tareas mecánicas, cortas y verificables. |

`claude-agent/opus` y `claude-agent/sonnet` son alias: hoy resuelven a Opus 5.5 y
Sonnet 5.5 y avanzarán solos con nuevas versiones (el catálogo `claude-agent` no
expone un ID versionado de la 5.5). Opus es escaso en el plan Pro: fuera de sus
dos defaults solo se usa mediante override del orchestrator (sección *Model
escalation* de `orchestrator.md`) para `security-reviewer` en dominios críticos
(`opus#high`) y `database-specialist` en migraciones irreversibles
(`opus#medium`). `debugger-deep` solo se invoca tras un `ESCALATE` del `debugger`
o dos arreglos fallidos y devuelve un plan de corrección que ejecuta un
implementer. Si el límite semanal se agota antes de tiempo, recorta en este
orden: `architect` a `opus#low`, escalada de seguridad a `opus#medium`, y
desactivar la escalada de `database-specialist`.

### Telemetría de tokens

`scripts/agent-telemetry.py` lee en solo lectura la base de datos local de
OpenCode (`~/.local/share/opencode/opencode.db`) y resume por agente: sesiones,
pasos por sesión, contexto inicial (prompt + esquemas de tools + handoff),
contexto máximo y tokens por paso. Úsalo para medir antes y después de cada
cambio de prompts, permisos o configuración, con la misma ventana temporal:

```sh
python3 -I scripts/agent-telemetry.py --days 7 --min-sessions 5
python3 -I scripts/agent-telemetry.py --since 2026-11-13 --by-model --agent implementer
python3 -I scripts/agent-telemetry.py --days 7 --json
```

La línea base previa a los cambios de compactación y `tool_output` está en
`telemetry/baseline-2026-11-13.txt`. Cambia una variable cada vez y compara.

### Skills del orchestrator

Las políticas del orchestrator que solo hacen falta en momentos concretos viven
en skills de carga bajo demanda; el prompt conserva un resumen con las
invariantes de seguridad y la instrucción de cargar la skill antes de actuar.
Así el cuerpo del orchestrator pasó de ~39k a ~29k caracteres (~2,5k tokens
menos en cada paso).

| Skill | Se carga antes de |
|---|---|
| `orch-commit` | invocar `git-manager` (commit, push, rama, PR, merge, sincronización) |
| `orch-parallel` | ejecutar un plan `TASKS` o lanzar subagentes en paralelo/segundo plano |
| `orch-memory` | llamadas a Engram con identidad de sesión/proyecto o estado de ejecución |
| `orch-escalation` | aplicar un override de `model` a un subagente |
| `orch-db-authority` | delegar cualquier operación contra una base de datos real |
| `orch-modes` | la primera etapa de una tarea (formato GUIDED/AUTO) |

El orchestrator carga sin preguntar solo `opencode` y `orch-*`; el resto de
skills queda en `ask`, de modo que las ve en su catálogo pero cargarlas él mismo
requiere aprobación. La
política de navegador sigue inline en cada agente a propósito: es una invariante
de seguridad que no debe depender de cargar una skill.

### Skills en subagentes

Todos los subagentes pueden cargar bajo demanda cualquier skill disponible
(`skill: "*"` allow), en particular las del proyecto en curso
(`.opencode/skills/`, `.claude/skills/`, `.agents/skills/`), sin que el
orchestrator tenga que nombrarlas. OpenCode filtra `skill` solo por ID, no por
ruta, así que se niegan por ID `orch-*` (política exclusiva del orchestrator) y
`report` (publica issues fuera). Una skill solo añade instrucciones: no amplía
los permisos de tools del perfil. La lectura directa de `*SKILL.md` sigue
denegada; las skills se cargan con la tool `skill`.

El catálogo nativo de OpenCode actúa como registry por proyecto, sin pasos de
refresh: OpenCode descubre las skills subiendo desde el cwd hasta la raíz del
worktree, más las globales, y a cada agente le anuncia solo las que no tiene en
`deny`. El orchestrator compara la tarea con las descripciones de ese catálogo y,
si alguna aplica, añade al handoff `Skills to load: <id>, <id>` (IDs exactos,
nunca rutas ni resúmenes). El subagente las carga antes de trabajar y puede
cargar otras relevantes por su cuenta. Una skill con
`metadata["opencode/autoinvoke"]: "false"` no se anuncia al modelo. Los implementers tienen
un tope de dos iteraciones de corrección por check fallido antes de devolver
`PARTIAL`/`ESCALATE`.

## Permisos de navegador (OpenCode)

Los perfiles `debugger`, `debugger-fast`, `debugger-deep`, `verifier` y `general-purpose` admiten
navegador **solo en OpenCode**, que es el runtime prioritario. Playwright y Chrome
DevTools se exponen como 25 acciones MCP exactas que quedan en `ask`; en esos
cinco perfiles `playwright_*` y `chrome-devtools_*` se deniegan por defecto, de
modo que una acción no listada (evaluación de código/JS,
`playwright_browser_run_code_unsafe`, detalles de red o cookies) sigue en `deny` y
no se convierte en un `ask` implícito. Los cuatro perfiles de implementación
(`implementer*` y `database-implementer`) conservan su default universal y piden
`ask` para cualquier acción browser desconocida.
`execute` está permitido en `debugger`, `debugger-fast`, `debugger-deep` y `verifier`, y en `ask` en
`general-purpose`; solo sirve para llamadas MCP exactas, y `fetch` sigue disponible
dentro de `execute`, nunca como shell ni para eludir una tool denegada.
`debugger`, `debugger-fast`, `debugger-deep` y `verifier` mantienen su regla previa de CodeGraph en
`allow`; los fallbacks anteriores no cambian.

Un `ask` —y el prompt que muestra OpenCode— es una puerta de política, **no un
sandbox**: no aísla el host, no demuestra que la operación sea segura y no limita lo
que una tool permitida puede hacer internamente. `allow always` persiste la decisión
en la configuración de OpenCode y puede alcanzar acciones o sesiones posteriores;
para una operación concreta elige **una vez** y no reutilices esa aprobación para
otra operación, página o destino.

Toda operación peligrosa requiere consentimiento **individual y exacto**: operación
y argumentos, página/URL/destino, motivo, impacto esperado, alcance y
recuperación/rollback. Cada operación posterior requiere su propio consentimiento;
una autorización de alcance o un `allow once` no se extienden a operaciones
relacionadas. No se promete ni se verificó comportamiento runtime: los permisos, el
forwarding de asks y la persistencia de aprobaciones dependen del host.

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
