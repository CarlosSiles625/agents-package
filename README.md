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
  reaparece; los renombrados no se detectan.

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
