# Linkcord

Cliente gráfico de Discord, liviano, que funciona en **Windows 7, 8, 10 y 11** con un solo `.exe`. Usa el código de [discordo](https://github.com/ayn2op/discordo) (cliente de Discord para la terminal, en Go) como base para la conexión, el inicio de sesión y la voz. La interfaz es propia: una ventana dibujada con [Gio](https://gioui.org).

Se hizo para que gente con Windows 7, que ya no puede usar la app oficial, tenga algo mejor que Discord en el navegador.

> ⚠️ **Términos de servicio:** usar un cliente no oficial va contra los términos de Discord. El riesgo de suspensión es bajo (igual que con discordo, la app se hace pasar por el cliente web), pero existe.

## Estado

| Funciona | Falta |
|---|---|
| Inicio de sesión: código QR, correo + contraseña (con código 2FA), token (con un script para copiarlo desde el navegador) | **Voz** (lo más importante: ver más abajo) |
| La sesión se guarda en el Administrador de credenciales de Windows; se guarda también el token nuevo cuando Discord lo renueva | |
| Servidores en el orden del usuario, mensajes directos, categorías plegables, permisos, no leídos y menciones | |
| Mensajes con formato de Discord, menciones, citas, código, spoilers, enlaces, imágenes en el chat, vistas previas de enlaces, insignia BOT | Emojis propios de los servidores dentro de los mensajes (se ven como `:nombre:`) |
| Visor de imágenes con zoom y arrastre | GIF animados, videos en el chat, stickers |
| Fotos de perfil e iconos (con caché en disco), estado en línea, lista de miembros | Hilos y foros |
| Perfiles (propios y ajenos), cambiar el estado | Lista de amigos, solicitudes, ajustes |
| Escribir: adjuntar archivos, selector de emojis, autocompletado de @menciones, "está escribiendo" | Probar en un Windows 7 real (ver "Pendiente") |
| Emojis a color en toda la app | |
| Acciones sobre mensajes (al pasar el ratón): reaccionar, responder (con o sin mención), editar en el sitio, copiar el texto y eliminar con confirmación (Mayús + clic la salta). Reacciones bajo los mensajes. ↑ en la caja vacía edita el último mensaje propio | |

## Compilar

Hace falta Windows y dos herramientas, que se instalan en la carpeta del usuario sin permisos de administrador:

1. **Go para Windows 7**: [go-legacy-win7 v1.27.2-1](https://github.com/thongtech/go-legacy-win7/releases/tag/v1.27.2-1), el archivo `go-legacy-win7-1.27.2-1.windows_amd64.zip`. Descomprimirlo en `%LOCALAPPDATA%\Programs\go-legacy-win7`. Es Go 1.27 con el soporte de Windows 7 que Go quitó en la versión 1.21. El Go normal compila, pero el `.exe` no abre en Windows 7.
2. **Compilador de C** (para el audio de la voz): [WinLibs GCC 16.2.0 con **MSVCRT**](https://github.com/brechtsanders/winlibs_mingw/releases/tag/16.2.0posix-14.0.0-msvcrt-r2), el archivo `winlibs-x86_64-posix-seh-gcc-16.2.0-mingw-w64msvcrt-14.0.0-r2.zip`. Descomprimir la carpeta `mingw64` en `%LOCALAPPDATA%\Programs\mingw64`. Tiene que ser la variante **MSVCRT** y no UCRT: MSVCRT viene con todos los Windows 7, UCRT no.

Después, desde esta carpeta:

```powershell
.\build-linkcord.ps1            # deja ..\linkcord.exe
.\build-linkcord.ps1 -Console   # con consola, para ver errores y panics
```

Pruebas:

```powershell
$env:PATH = "$env:LOCALAPPDATA\Programs\go-legacy-win7\bin;$env:LOCALAPPDATA\Programs\mingw64\bin;$env:PATH"
$env:CGO_ENABLED = "1"
go test ./internal/gui ./internal/auth
go vet ./internal/gui ./internal/auth ./cmd/linkcord
```

## Probar sin cuenta y sin Windows 7

| Variable de entorno | Para qué |
|---|---|
| `LINKCORD_DEMO=1` | Abre con servidores, personas y mensajes inventados, sin conectarse a Discord. Sirve para revisar la interfaz. Los datos están en `internal/gui/demo.go`. |
| `GIO_LEGACY_MOUSE=1` | Usa los mensajes de ratón de Windows 7 en vez de los de Windows 8+, para probar ese camino en Windows 10/11. |
| `GIO_D3D11_WARP=1` | Dibuja sin tarjeta gráfica (WARP), como pasa en muchos Windows 7 viejos o en máquinas virtuales. |
| `LINKCORD_TOKEN=...` | Usa ese token en vez del guardado. |

`scripts\probar-ventana.ps1` abre la app y le manda clics, rueda del ratón y teclas **directamente a su ventana**, sin mover el ratón real, y guarda capturas aunque haya otras ventanas encima. Así se revisaron todos los cambios de la interfaz. El uso está al principio del script. Ejemplo:

```powershell
.\scripts\probar-ventana.ps1 -Demo -Script { Start-Sleep 4; Snap "inicio"; MouseClick 500 632; TypeText "@an"; Snap "menciones" }
```

Archivos en la PC del usuario:
- Registro: `%LOCALAPPDATA%\linkcord\logs.txt`
- Caché de imágenes: `%LOCALAPPDATA%\linkcord\imagenes`
- Token: en el Administrador de credenciales de Windows, con el nombre `linkcord`.

## Cómo está organizado

Todo lo nuevo está en `cmd/linkcord`, `internal/gui`, `internal/auth` y `third_party`. Del código de discordo solo se tocaron 4 archivos: `go.mod`, `go.sum`, `internal/http/client.go` y `internal/http/client_standard.go` (se agregó `NewSessionClient`). Para traer actualizaciones de discordo hay que cambiar en ellas la ruta del módulo (`github.com/ayn2op/discordo` → `github.com/ezchr/linkcord`); fuera de eso, casi no hay conflictos. Importa porque Discord cambia cosas a menudo.

| Archivo | Qué hace |
|---|---|
| `cmd/linkcord/main.go` | Punto de entrada. |
| `internal/auth/` | Inicio de sesión sin interfaz: `qr.go` (QR), `password.go` (correo + 2FA), `client.go` (cliente que abre Discord primero para recibir cookies y `X-Fingerprint`, así Discord pide captcha con menos frecuencia), `token.go` (Administrador de credenciales). |
| `internal/gui/app.go` | La ventana, el bucle de eventos y las pantallas (login, conectando, chat). Contiene `post()`, que es la única forma segura de pasar trabajo de otra goroutine a la ventana. |
| `internal/gui/session.go` | Arma el estado de Discord (arikawa + ningen) igual que discordo. Registra el evento `AUTH_TOKEN_REFRESH`, que arikawa no conoce. |
| `internal/gui/chat.go` | La pantalla principal: servidores, canales, mensajes directos, panel del usuario, y los eventos del gateway (`pump` → `apply`). |
| `internal/gui/messages.go` | Historial, mensajes, adjuntos, imágenes en el chat, embeds, la barra de arriba. |
| `internal/gui/markdown.go` | Formato de Discord con `gioui.org/x/richtext`: negrita, cursiva, código, citas, spoilers, enlaces, menciones. |
| `internal/gui/composer.go` | La caja de texto: enviar, adjuntar, autocompletado de @, "está escribiendo". |
| `internal/gui/emoji.go` | Selector de emojis (la lista con palabras de búsqueda está ahí). También elige la reacción de un mensaje. |
| `internal/gui/actions.go` | Acciones sobre mensajes: barra al pasar el ratón, responder, editar, eliminar, copiar y reacciones. |
| `internal/gui/render_test.go` | Dibuja el modo demo sin ventana y guarda imágenes; sirve cuando no hay pantalla. Ver el comentario de `TestRenderDemo`. |
| `internal/gui/members.go` | Lista de miembros (la lista perezosa de Discord, a través de ningen). |
| `internal/gui/profile.go` | Tarjetas de perfil y menú de estado. |
| `internal/gui/presence.go` | Estado en línea, actividades, puntos de estado. |
| `internal/gui/viewer.go` | Visor de imágenes. |
| `internal/gui/images.go` | Descarga de imágenes en segundo plano, reducidas al tamaño en que se ven, con caché en memoria y en disco. |
| `internal/gui/theme.go` | Colores y fuentes (ver "Fuentes y emojis"). |
| `internal/gui/widgets.go` | Piezas comunes: filas, botones, avatares, `vcenter`… |
| `internal/gui/voice.go` | **Vacío por ahora**: aquí va la voz. |
| `internal/gui/demo.go` | Datos del modo demo. |

**Regla de hilos:** el estado de la interfaz solo se toca desde la goroutine de la ventana. Lo que llega de Discord o de una descarga en segundo plano pasa por `c.u.post(func() { ... })`. Gio redibuja cuando hay eventos; `u.win.Invalidate()` pide un cuadro nuevo.

## Parches a librerías (`third_party`)

`go.mod` usa copias locales de dos librerías, con parches. Los cambios están también en `third_party/parches/*.patch`, para poder aplicarlos a versiones nuevas.

1. **Gio v0.10.3** (`third_party/gioui`), para Windows 7:
   - `SetThreadDpiAwarenessContext` (Windows 10) y `EnableMouseInPointer`/`GetPointerInfo` (Windows 8) se llamaban sin comprobar que existieran, y la app se cerraba en Windows 7. Ahora se comprueban, y cuando faltan el ratón usa los mensajes clásicos (`WM_MOUSEMOVE`, `WM_LBUTTONDOWN`, `WM_MOUSEWHEEL`…).
   - Si Direct3D 11 por hardware falla, se usa WARP (dibujo por software).
2. **gioui.org/x v0.10.3** (`third_party/giox`): `richtext` no dibujaba los glifos que son imágenes, y los emojis a color quedaban invisibles en los mensajes.

## Fuentes y emojis

- Los emojis a color salen de **Noto Color Emoji** (`internal/gui/fonts`, licencia OFL), incluida en el `.exe` (unos 10 MB). Usa imágenes PNG (tabla CBDT), que Gio sí sabe dibujar. **Segoe UI Emoji no sirve**: usa capas de color (COLR) que Gio no dibuja, y los emojis quedan en blanco.
- Al cargarla se angosta su espacio (`narrowSpace` en `theme.go`): el dibujador de texto deja los espacios que siguen a un emoji en la fuente del emoji, y el espacio de Noto es tan ancho como un emoji. Hay una prueba en `theme_test.go`.
- Detrás van Segoe UI Symbol y Cambria Math, de Windows, para símbolos y letras decorativas (𝓐𝓑𝓒, ᴬᴮᶜ). **No hay que fijar `th.Face`**: con una tipografía fija, el texto en negrita deja de buscar en las otras fuentes y salen cuadros (□).

## Trampas de Gio que ya costaron tiempo

- `layout.W` deja la altura mínima del área al hijo: un texto queda tan alto como la barra y se dibuja arriba. Para centrar verticalmente hay que usar `vcenter` (en `widgets.go`).
- En un `layout.Stack`, un hijo `Expanded` que devuelve tamaño cero se centra como si fuera un punto y se corre hacia abajo. Hay que devolver `gtx.Constraints.Min`.
- Para que una ventana emergente no deje pasar clics a lo que tiene debajo, hay que registrar un manejador en toda su área: `solid()` en `profile.go`.
- Para interceptar teclas de la caja de texto (flechas en la lista de @), hay que pedir los eventos con `key.Filter{Focus: &c.composer, ...}` **antes** de `composer.Update`.
- En PowerShell 5, `Get-Content`/`Set-Content` sin `-Encoding` estropean los acentos. Para editar archivos desde scripts conviene usar `[IO.File]::ReadAllText`/`WriteAllText` con UTF-8 sin BOM.

## Cosas de Discord a tener en cuenta

- **Captcha:** a veces Discord pide un hCaptcha al iniciar sesión, y la app no puede mostrarlo. Por eso existe el inicio con token y el script para copiarlo desde el navegador (pestaña Token). El hCaptcha no se puede mostrar en una página local porque está atado al dominio de Discord.
- `ReadyEvent.UserSettings` puede venir vacío: hay que comprobarlo.
- El token se renueva con `AUTH_TOKEN_REFRESH` (en `session.go`). Si no se guarda el nuevo, la sesión puede perderse.
- No hay que pedir perfiles de forma masiva: solo cuando el usuario abre uno. Discord vigila eso en los clientes no oficiales.

## Pendiente, por prioridad

1. **Voz.** `internal/voice` (de discordo) ya hace todo lo difícil: conexión, Opus, cifrado DAVE, micrófono y altavoces con miniaudio (que funciona en Windows 7). Falta conectarlo en `internal/gui/voice.go`: unirse al hacer clic en un canal de voz (`joinVoice`), un panel sobre el del usuario con "Conectado a…" y el botón para salir (`layoutVoicePanel`), los botones de silenciar y ensordecer (`layoutVoiceButtons`), y el borde verde de quien habla (`voiceSpeaking`). En discordo se puede ver cómo lo usa: `internal/ui/chat/guildstree`. Necesita compilar con cgo, cosa que el script ya hace.
~~2. Acciones sobre mensajes~~ (hecho).
3. **Probar en un Windows 7 de verdad.** Revisar sobre todo que los certificados raíz del sistema permitan las conexiones TLS. Si un Windows 7 sin actualizaciones no tiene los certificados de Discord, habría que incluir los propios.
4. Emojis de servidor dentro de los mensajes como imágenes (hoy `:nombre:`), stickers y GIF animados.
5. Contador de mensajes no leídos y de menciones en servidores y mensajes directos. Notificaciones con vista previa.
6. Hilos, foros, lista de amigos, ajustes (tamaño de letra, notificaciones), placa de nombre, efectos de perfil.
7. Icono y manifiesto del `.exe`, instalador o actualizador.

## Licencias

- Como usa código de discordo, que es GPL-3.0, Linkcord también es **GPL-3.0** (ver `LICENSE`). Si se reparte el `.exe`, hay que poder entregar el código fuente a quien lo pida.
- Gio: MIT / Unlicense.
- Noto Color Emoji: SIL Open Font License 1.1 (`internal/gui/fonts/OFL.txt`).
