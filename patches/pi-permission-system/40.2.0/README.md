# Parche MANUAL `patched-40.2.0-v1`

Solo para **@gotgenes/pi-permission-system@40.2.0**. Este directorio es un
artefacto de revisión: no instala plugins ni modifica un paquete activo.

Fuente original inmutable recuperada por HTTP de lectura:
`https://cdn.jsdelivr.net/npm/@gotgenes/pi-permission-system@40.2.0/src/config/yaml-frontmatter.ts`.
`upstream/yaml-frontmatter.ts` conserva exactamente esos bytes. El diff
`patched-40.2.0-v1.patch` apunta únicamente a `src/config/yaml-frontmatter.ts`.
`patched/yaml-frontmatter.ts` sirve como referencia del resultado exacto, no como
un loader alternativo. SHA-256:

- Original: `08b8aaaec8854c536d524a4db78f9ee884684c53d711558374c639b4d05b43c4`.
- Patched: `b71cf626341d397b5db00f40c6081d51ddc9c260d63b878e8d67966bac5047f0`.

## Contrato

El separador se encuentra **después de la clave completa**, fuera de comillas.
Claves double-quoted decodifican el subconjunto JSON de escapes; claves
single-quoted decodifican `''` como `'` y conservan backslashes literalmente.
No se cambian valores escalares, maps anidados, orden de inserción de claves ni
last-match-wins. No se añade YAML general (anchors, arrays, multiline, etc.).
Claves vacías, malformadas, quotes sin cerrar, escapes no soportados, controles
y Unicode con surrogates aislados lanzan error; nunca se devuelve un mapa
parcial. Claves decodificadas son propiedades propias, no setters de prototipo.

## Validación efímera

Desde la raíz de agents-package, con Node que soporte native TypeScript strip
(validado con Node 24.21.0), sin npm install ni plugins:

```sh
node patches/pi-permission-system/40.2.0/validate.mjs --apply-only
node patches/pi-permission-system/40.2.0/validate.mjs --test
```

El script comprueba SHA de fixtures, crea una copia privada bajo
`/tmp/opencode`, aplica el diff sin fuzz, verifica el SHA y ejecuta regresiones
sobre el **parser resultante de aplicar el diff**. Al terminar elimina solo su
directorio efímero. Las regresiones comparan todas las claves reales de
git-manager (incluidas las cinco con `:` y los backslashes), decisiones/orden,
escapes, Unicode, maps y errores. No contacta paquetes instalados ni HOME.

## Aplicación manual futura (NO ejecutada por agent-sync)

Requiere revisión de seguridad y autorización de aplicación aparte. Primero
identifica la raíz **exacta** del paquete 40.2.0 que carga tu host; no se asume
una ruta npm global/proyecto. Si el host usa un bundle/precompilado que no lee
este archivo TS, el parche de fuente no demuestra activación runtime.

1. Guarda una copia byte-exacta del archivo original en una ruta privada revisada
   para rollback. Comprueba versión del paquete y SHA original; **para si no
   coincide**. Un upgrade invalida este parche y la declaración CLI.
2. Desde esa raíz revisada, inspecciona un dry-run:

   ```sh
   sha256sum src/config/yaml-frontmatter.ts
   patch --dry-run --batch --forward --fuzz=0 -p1 < /ruta/artefacto/patched-40.2.0-v1.patch
   ```

3. Solo con autorización de escritura del archivo exacto, aplica el mismo
   comando sin `--dry-run`. Verifica que el SHA resultante es el **patched** de
   arriba; para ante cualquier diferencia. Reinicia el host y verifica su loader
   antes de usar agentes que dependan del parche. No mezcles versiones/scopes.
4. En agent-sync declara lo realizado seleccionando
   `--permission-parser patched-40.2.0-v1` en install/pull/status/recover. Este
   flag **declara que el usuario aplicó y verificó el parche**; el CLI no lo
   instala, comprueba ni activa. No requiere una segunda attestation.

Rollback: restaura únicamente la copia original verificada en el archivo exacto
revisado y confirma su SHA antes de reiniciar el host. No uses exports patched
con un parser restaurado/upgraded. El estado CLI fija el modo y rechaza cambiarlo
silenciosamente; no borres estado para eludir esa protección. La migración de
un destino existente entre modos no está automatizada.
