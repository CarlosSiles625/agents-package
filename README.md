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
se pueden cambiar agentes gestionados (presentes en el baseline); `opencode.json`,
skills, MCP y credenciales quedan fuera.

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

## Pi: exportación aislada y recuperable

El flujo OpenCode anterior permanece igual. Pi usa un **baseline convertido y
estado separado**, sin modificar los agentes fuente, `AGENTS.md`, configuración
OpenCode, configuración activa Pi ni instalar extensiones.

```sh
agent-sync pi install --scope project --target /ruta/proyecto --repo /ruta/repo --branch main --dry-run
# Tras revisar y autorizar el plan:
agent-sync pi install --scope project --target /ruta/proyecto --repo /ruta/repo --branch main --yes
agent-sync pi status  --scope project --target /ruta/proyecto
agent-sync pi pull    --scope project --target /ruta/proyecto --yes
agent-sync pi recover --scope project --target /ruta/proyecto

agent-sync pi install --scope global --repo /ruta/repo --branch main --yes
agent-sync pi status  --scope global
```

- `--scope` es obligatorio: `project` escribe agentes en
  `<target>/.pi/agents` (el proyecto actual si se omite `--target`); `global` en
  `<target>/agents`, donde `--target` es el **directorio Pi de usuario**, no un
  proyecto. Sin override: `$PI_CODING_AGENT_DIR/agents`, o
  `~/.pi/agent/agents`. Se expande `~/` en esa variable.
- `--repo`, `--branch`, `--dry-run`, `--yes`, `--replace <ruta>` y
  `--allow-delete` conservan el flujo existente: se lee el **commit Git
  seleccionado**, nunca el working tree. `status` no hace fetch. Pi no admite
  `models`, `--add-to-path` ni `--shell`.
- `--permission-parser upstream|patched-40.2.0-v1` (default `upstream`) fija la
  compatibilidad del parser YAML del permission system. `patched-40.2.0-v1` es
  una **declaración de usuario**, no una instalación ni verificación runtime; el
  modo se guarda en el estado y un cambio silencioso se rechaza (repite el mismo
  valor en pull/status/recover). Detalles abajo.
- Estado: `<base>/pi/<scope>-<hash-del-destino>/`, con `base` igual al estado
  OpenCode por defecto o a `--state BASE`. Este último es una **base de
  namespaces**, no el archivo de estado OpenCode. Repite scope, target y base
  en pull/status/recover. El bloqueo, revalidación, journal, backups y recuperación
  son los existentes; modificaciones locales se conservan y una divergencia
  local/remota aborta todo el apply. Pull idéntico no reescribe los archivos Pi.
- Las rutas de `--replace` son relativas al directorio Pi efectivo, por ejemplo
  `agents/researcher.md`. Los borrados requieren `--allow-delete`.

### Artefactos, no configuración activa

Un snapshot convertible produce subagentes cargables y primary separados.

**Rechazo del snapshot actual y parser de permisos.** Por defecto
(`--permission-parser upstream`) el parser YAML de
`pi-permission-system@40.2.0` no admite `:` dentro de las claves de patrones, ni
entre comillas: las cinco reglas de `git-manager.md` con `:` no son
representables fielmente. Además, `implementer.md`, `implementer-fast.md`,
`implementer-deep.md` y `database-implementer.md` declaran un default universal
explícito `*: allow`; el allowlist CSV cerrado de los subagentes Pi no puede
expresar tools presentes o futuras (MCP incluido), por lo que el export completo
se rechaza **en ambos modos de parser** y **antes** de escribir
destino/baseline. No se elimina ni recorta ningún agente, no se enumeran MCP de
forma heurística y no se genera un inventario parcial.

Además, los cinco perfiles browser (`debugger`, `debugger-fast`, `debugger-deep`, `verifier` y
`general-purpose`) usan acciones MCP sin mapping Pi respaldado por evidencia
(`playwright_*`, `chrome-devtools_*`); cada uno se rechaza de forma atómica en
ambos modos. El snapshot completo ya fallaba antes por el `*: allow` de los
implementers, así que estos perfiles nuevos tampoco son exportables a Pi y no se
inventan mappings para conseguirlo. El subconjunto de parser que sí convierte
(10 subagentes cargables) excluye ambos grupos solo en las pruebas; no se filtra
ni recorta la fuente en producción.

La alternativa aprobada
es `--permission-parser patched-40.2.0-v1`, que declara que el usuario **ya
aplicó y verificó por SHA-256** el parche versionado de este repositorio
(`patches/pi-permission-system/40.2.0/patched-40.2.0-v1.patch`; hashes,
procedimiento manual y rollback en ese directorio). El CLI **no** instala, aplica
ni verifica en runtime el parche, y solo en ese modo exporta claves con `:` como
double-quoted JSON exactas; no convierte un allow universal en un allowlist
finito. El modo forma parte del estado/baseline gestionado: no mezcles modos y
no borres estado para eludir el rechazo de cambio silencioso. Un upgrade de
`pi-permission-system` invalida el parche y la declaración.

Para un snapshot convertible, bajo el directorio Pi efectivo se generan:

| Ruta | Uso |
| --- | --- |
| `agents/**/*.md` | YAML Pi y prompt de cada subagente |
| `agent-sync/primary/orchestrator.md` | cuerpo primary, fuera del loader de subagentes |
| `agent-sync/policies/orchestrator.json` | política root para revisión/aplicación **MANUAL** |
| `agent-sync/web-search.global.manual.json` | perfil web **MANUAL y solo global**, también exportado con scope project |
| `agent-sync/report.md` | conversiones, capacidades externas pendientes y pasos manuales |

No se selecciona un primary automáticamente ni se fabrica un tag
`active_agent`. Desde el directorio Pi que contiene `agent-sync/`, después de
revisar/aplicar manualmente las políticas y extensiones:

```sh
env -u PI_ALLOW_BROWSER_COOKIES -u FEYNMAN_ALLOW_BROWSER_COOKIES \
  pi --append-system-prompt agent-sync/primary/orchestrator.md
```

En Windows elimina ambas variables del entorno del proceso antes de iniciar Pi.
`PI_ALLOW_BROWSER_COOKIES=1` **o** `FEYNMAN_ALLOW_BROWSER_COOKIES=1` prevalece sobre
`allowBrowserCookies:false`. El archivo append no activa la política JSON y
no aplica un presupuesto de turnos al root.

### Extensiones y configuración MANUAL

Requiere **Pi >=1.0 y Node >=22.19.0** (el paquete web exige 22.19), con estas
versiones exactas. Comandos de referencia para ejecutar **solo después de
revisión/autorización**, no ejecutados por agent-sync:

```sh
pi install npm:@gotgenes/pi-subagents@23.4.0
pi install npm:@gotgenes/pi-permission-system@40.2.0
pi install npm:pi-web-access@0.39.0
```

Revisa la política root JSON antes de incorporarla manualmente al config del
permission system: `<Pi-global>/extensions/pi-permission-system/config.json`
o `<proyecto>/.pi/extensions/pi-permission-system/config.json`. No sobrescribas
configuración ajena. La política global afecta otras sesiones; la política y
los agentes de proyecto requieren la confianza del proyecto del host.

El perfil web se revisa y aplica **solo al `web-search.json` global** que
seleccione pi-web-access: `PI_CODING_AGENT_DIR` tiene prioridad; sin esa variable
puede seleccionar un archivo XDG existente (`$XDG_CONFIG_HOME/pi/web-search.json`)
o uno legacy (`~/.pi/web-search.json`), además de `~/.pi/agent/web-search.json`.
Comprueba la ruta efectiva y las claves existentes antes de cualquier aplicación
manual. No se genera un perfil web activo de proyecto. Reinicia Pi después.

El perfil aprobado admite únicamente Exa para búsqueda, fetch HTTP directo,
modos readable/raw y parseo unpdf **local de PDFs descargados**. Desactiva
cookies/authFetch, video/YouTube, especializaciones GitHub clone/`gh`, providers
de fetch hosted, source_check/get_search_content y comandos web. SSRF conserva
rangos adicionales vacíos y `trustEnvProxy:false`. No contiene credenciales.
`includeContent` puede hacer fetch HTTP(S) interno de los resultados; forma parte
de la autorización web del researcher. El texto inline largo puede truncarse;
se aceptó no habilitar get_search_content.

**El perfil manual es necesario para estas restricciones de capacidades: los
grants por sí solos no controlan los efectos internos de una extensión.** Usa
solo URLs HTTP(S), nunca rutas locales/uploads, auth, proxies o shell/curl como
sustituto. Mantén `workflow:none` en las llamadas, no pidas workflows de resumen
o navegador: el default no es una prohibición de overrides por llamada. No
añadas resolvers de credenciales ejecutables, authFetch ni funciones extra al
perfil. Esto **no es un sandbox** ni aislamiento del sistema operativo.

### Contrato de conversión y límites

- `model`, `variant` y thinking no se exportan: todos los agentes indican
  **INHERIT del modelo Pi activo**. No se traducen nombres OpenCode a modelos Pi
  ni se valida un catálogo. Un caller Pi puede elegir overrides propios.
- `steps` es configuración numérica, no un filtro de resultados: se adapta a
  `max_turns`, mínimo 2; cero o ausente omite el límite. Pi cuenta turnos de modelo
  exitosos y detiene al alcanzar el ceiling: no equivale exactamente a steps
  OpenCode. Los defaults del host/caller pueden imponer otro límite.
- `permissions` LIST ordenada se convierte en `permission` MAP de scalars/maps
  ordenados. Se conserva last-match-wins, incluyendo wildcards `*` que cruzan
  `/`. Duplicados se conservan en su **última** posición solo cuando se prueba
  equivalencia; interleavings o enumeración de claves numéricas que cambiarían
  decisiones se rechazan antes de escribir destino/baseline. El fetch puede
  crear caché/lock; no hay export parcial de agentes.
- El parser YAML mínimo del plugin fijado no decodifica escapes ni respeta
  comillas al buscar `:`. En modo `upstream` el export conserva backslashes con
  quoting literal y rechaza claves con `:` o que necesiten escapes que ese
  parser no entiende. El modo `patched-40.2.0-v1` (declaración de parche
  aplicado) exporta esas claves como double-quoted JSON exactas, sin convertir
  comillas/backslashes/`:` en wildcards. JSON root MANUAL sí admite esas claves;
  no es un sustituto de permisos específicos de un subagente.
- `shell→bash`, `glob→find`, `list→ls`; `edit` no concede `write`. Write solo se
  admite con permiso explícito. Sin overrides path se añade `path:allow` neutral:
  no vence denegaciones de tools ni `read:*SKILL.md` o external_directory. Las
  variantes direccionales conservan su orden. Un default source omitido se
  representa explícitamente como el default documentado OpenCode `*:allow`,
  no como el `ask` Pi, conservando el comportamiento histórico; no se importa
  configuración global OpenCode ni sus reglas implícitas `.env`. Un `*: allow`
  **explícito** en un subagente se rechaza de forma atómica: el CSV cerrado no
  puede expresar tools futuras/MCP y el export no debe perder capacidades ni
  inventar una allowlist. Revisa esta diferencia en la política root MANUAL.
- `websearch→web_search`, `webfetch→fetch_content`. Los aliases de recursos
  `opencode_list_mcp_resources→list_mcp_resources` y
  `opencode_read_mcp_resource→read_mcp_resource` requieren **esas tools exactas**
  en el host. Se admiten únicamente mappings MCP documentados Context7,
  CodeGraph, Engram y las operaciones Supabase acotadas de la fuente. Nombres o
  recursos sin equivalencia segura fallan claramente, nunca se descartan.
- Los allowlists CSV admiten tools nativas y MCP exactas/patrones acotados
  `mcp__server__tool`; no pueden expresar un allow universal ni tools futuras,
  por lo que un `*: allow` explícito en la fuente se rechaza antes de generar
  artefactos. Conecta los servidores antes de spawn y usa exposición
  MCP **direct**, no codemode. Un server ausente no aporta tools. No se instala
  ni configura MCP; el reporte declara estas dependencias sin inventarlas.
  `execute`/codemode no se conceden: no hay bypass genérico a shell. La tool
  `question` no tiene equivalente nativo; devuelve la pregunta al parent en la
  respuesta. No se amplían permisos para suplir capacidades ausentes.
- Los hijos heredan la extensión de permisos: **no** excluirla mediante
  `excludedExtensionPackages` ni activar `yoloMode`. El runtime aporta identidad
  `<active_agent>` y lifecycle para forwarding de asks a la UI root. La ausencia
  de recursión en hijos no elimina delegación en root.

No se han instalado/probado los plugins reales. Identidad, UI de asks, trust,
exposición MCP y comportamiento conjunto requieren validación posterior en el
host autorizado. Fuentes de contrato: documentación/schema publicados de
`@gotgenes/pi-permission-system@40.2.0`, configuración de
`@gotgenes/pi-subagents@23.4.0` y README de `pi-web-access@0.39.0`.

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
