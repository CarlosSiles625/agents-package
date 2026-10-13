# T12 / T21 — validación de la implementación Pi

Estado: **PARTIAL**. Owner de los checks T1/T2: **T21** (continuación del owner
T12). `CHK-T3-SEC` pertenece a **security-reviewer** y no lo ejecuta este owner.
CWD de todos los comandos: `/home/carl/Projects/agents-package`.

## Entorno y autorización

- Se autorizó descargar/usar Go 1.27.0. Los comandos usan
  `GOTOOLCHAIN=go1.27.0` de forma local al proceso; la primera ejecución descargó
  esa toolchain linux/amd64. No se modificó `go.mod` ni configuración persistente.
- Módulo: `agent-sync`, requisito `go 1.27`, sin nuevas dependencias del módulo.
- Node `v24.21.0` con native TypeScript strip; GNU `patch 2.8`. La validación del
  parche usa solo copias privadas efímeras bajo `/tmp/opencode`; **no** hubo
  `npm install`, ni se leyeron paquetes/plugins instalados ni HOME.
- No se instalaron plugins, no se tocaron configs activos ni credenciales, no se
  aplicó el parche a ningún paquete real. Los fixtures de sincronización usan
  repositorios/targets/estado temporales.
- Sin mutaciones Git. Cambios ajenos preservados: `install.sh`
  (`c09cdabc…55c25d`) y `README.m` sin cambios.

## Checks y evidencia

| ID | Comando | Scope | Exit | Resultado / evidencia |
| --- | --- | --- | --- | --- |
| CHK-T1-APPLY | `node patches/pi-permission-system/40.2.0/validate.mjs --apply-only` | artefacto parche/hash/aplicación efímera | 0 | PASS. Verifica SHA-256 de fixtures, aplica `patched-40.2.0-v1.patch` con `--fuzz=0` a una copia privada en `/tmp/opencode` y confirma bytes idénticos. `original=08b8aaa…b43c4 patched=b71cf62…047f0`. Ningún archivo activo tocado. |
| CHK-T1-TS | `node patches/pi-permission-system/40.2.0/validate.mjs --test` | regresión del parser resultante | 0 | PASS, 4 tests. Reaplica el diff y ejecuta `regression.ts` sobre el parser **resultante**: cinco claves reales con `:` de `git-manager.md`, todas las claves de comando/backslash y last-match-wins; escapes single/double, backslashes literales y Unicode como claves exactas; maps anidados/valores/orden/frontmatter sin cambios; claves malformadas/sin cerrar/`__proto__`/surrogates lanzan error sin política parcial. `upstream` reproduce la regresión de `:`. |
| CHK-T2-GO | `TMPDIR=/tmp/opencode GOTOOLCHAIN=go1.27.0 go test ./internal/piexport ./cmd/agent-sync -count=1` | converter/permisos/modo + CLI Pi | 0 | PASS. `ok agent-sync/internal/piexport 0.010s`, `ok agent-sync/cmd/agent-sync 1.957s`. Incluye rechazo atómico del snapshot de 17 en `upstream`, export de 16 subagentes + primary en `patched-40.2.0-v1`, round-trip de claves, e2e de declaración/modo en estado, idempotencia, recovery y rechazo de cambio silencioso de modo. |
| CHK-T2-BUILD | `TMPDIR=/tmp/opencode GOTOOLCHAIN=go1.27.0 go build ./cmd/... ./internal/...` | build acotado | 0 | PASS, sin salida. Evita el paquete raíz preexistente `README.m` (Objective-C). |

Evidencia histórica (T12, código previo al modo parser) — **superada** para el
scope de código afectado por CHK-T2-GO:

| ID | Comando | Scope | Exit | Resultado |
| --- | --- | --- | --- | --- |
| P1-27-1 (histórico) | `GOTOOLCHAIN=go1.27.0 go test ./internal/piexport -count=1 -v` | converter | 0 | PASS, 10 tests; código anterior al flag de parser. |
| P2-27-1 (histórico) | `GOTOOLCHAIN=go1.27.0 go test ./cmd/agent-sync -run '^TestPi' -count=1 -v` | CLI Pi | 0 | PASS; código anterior al flag de parser. |
| P1-LOCAL-0 (histórico) | `go test ./internal/piexport -count=1 -v` con Go 1.26.9 local | converter | 1 | No satisface `go 1.27`; no validó código. |

### Regresión funcional del modo patched (CHK-T2-GO / CHK-T1-TS)

- `upstream` (default) rechaza las cinco claves `:` de `git-manager.md` **antes**
  de escribir destino/baseline; no hay inventario parcial ni filtrado de agente.
- `patched-40.2.0-v1` exporta 16 subagentes cargables + primary separado sin
  descartar claves; el report/baseline declara el modo y el estado lo fija.
- El CLI **no** instala, aplica ni verifica en runtime el parche: es una
  declaración de usuario. Un upgrade de `pi-permission-system` la invalida.

## Snapshot de código validado (SHA-256)

Estado dirty al ejecutar los checks: modificaciones propias en `README.md`,
`cmd/agent-sync/main.go`, `internal/state/state.go`; nuevos
`cmd/agent-sync/pi.go`, `pi_test.go`, `internal/piexport/*` y
`patches/pi-permission-system/40.2.0/*`. `install.sh` es un cambio ajeno
preservado. No basta HEAD: los hashes siguientes identifican los bytes locales
usados por CHK-T2-GO/CHK-T2-BUILD.

```text
c09cdabc4071bed2d516763d536e17addd7896c63d4f22901cba764c8655c25d  install.sh
8efcdfaf06bfe2f6d5cf8afe50e9fcfabc7231b5f01032d426b6094346a942a7  go.mod
3576a54b1daf7f93152383e947f8a7b5c24d9072b6075ccb3dfabefa6edf1835  README.m
265ce0040e377269fcdc896c6b4707e1bbd1d2295191adc432cdb8cde878dea2  README.md
1ca7516c2047ca0c8945d135d61b492f4391be84eee0ae173c57c587f5ebb835  cmd/agent-sync/main.go
99877b6a6cf601b0d6c1b732ccae25aff24fe51dcfb60d3657fc3a441aaa09ef  cmd/agent-sync/pi.go
c1dc9a2f6f0f6615575ad7f8f9079113f5b3f90ae54c021ee40a5ca0a6720cf1  cmd/agent-sync/pi_test.go
1398f05e49589864b4cd885937f9fe70577f0eb7888f9c1611045453e930496b  internal/state/state.go
45bf6c422ad9d6b7a148c1ea7a19f10f50c0de776bcfe548c5251a42dd835ec8  internal/piexport/parse.go
6d796172c8c736bba0f268cbd021d9e61998122983e48c36b90234d981bb51b2  internal/piexport/permissions.go
bcbe1494c20160ef88d52b787cdb6af5e6e57120a0fe948c433eacffb966586c  internal/piexport/parser_mode.go
8109f0323564eab4b71a96a8dfd605047dc796bfcb8ffb12ae6b0b3ed883725f  internal/piexport/export.go
70e17edc539058ad1a48e9ae187d4eebcbca4c1e005527073fe97a557b4773f6  internal/piexport/export_test.go
dbc56d44ba12b597e6617175e9e9a6cbd1450b5e0ae542142df7ad51d3d06dfd  internal/piexport/web-search.global.manual.json
08b8aaaec8854c536d524a4db78f9ee884684c53d711558374c639b4d05b43c4  patches/pi-permission-system/40.2.0/upstream/yaml-frontmatter.ts
b71cf626341d397b5db00f40c6081d51ddc9c260d63b878e8d67966bac5047f0  patches/pi-permission-system/40.2.0/patched/yaml-frontmatter.ts
4e76a24f4638be377b3ea20346186ba8f2dc50cfc74a3451ef08055d82af58f8  patches/pi-permission-system/40.2.0/patched-40.2.0-v1.patch
6233439d9963c6e8ea495ff7384309667c30a28e530844f3e3df3612f4d62a9a  patches/pi-permission-system/40.2.0/regression.ts
010a2f29f17daf473cafbff606c80cd8d8dfc4929a8ad1afd5fe9274f65bd530  patches/pi-permission-system/40.2.0/validate.mjs
ccf6c7c6abd8eb8998ff75d45873268b45f70deaf00a3056dab5add202a50ad2  patches/pi-permission-system/40.2.0/README.md
35bef5bce7a4476ee703ad2f1f4346bac39acd93f412d9bb349af899f62c2c31  patches/pi-permission-system/40.2.0/SHA256SUMS
```

Los hashes de `install.sh` y `go.mod` coinciden con el estado recibido. Este
documento no afecta el código ni la configuración usada en los checks.

## Aceptación y trabajo restante

1. **CHK-T3-SEC — PENDIENTE (security-reviewer, no este owner):** revisión
   estática de seguridad del parche, del modo `patched-40.2.0-v1`, del parsing
   de claves y de la declaración de usuario. No ejecutada aquí.
2. **Integración runtime — PENDIENTE (host autorizado/verifier):** instalación y
   carga reales de `pi-permission-system@40.2.0`, identidad, UI de asks, trust,
   exposición MCP y comportamiento conjunto. No se instalaron plugins ni se
   aplicó el parche a un paquete real; su ausencia no se trata como fallo de este
   código.
3. **`README.m` raíz — problema preexistente no tocado:** Go lo interpreta como
   Objective-C; por eso los checks se acotan a `./cmd/... ./internal/...`. El
   archivo no se renombró, excluyó ni eliminó; `git status` no lo muestra
   modificado. Corregirlo requiere autorización explícita (owner:
   parent/mantenedor).
4. Sin commits: los cambios quedan sin commitear para parent/`git-manager`.
