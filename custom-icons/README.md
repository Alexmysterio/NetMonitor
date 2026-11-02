# Iconos personalizados

Coloca aquí tus iconos (`SVG`, `PNG`, `WebP` o `JPG`). Docker Compose monta esta carpeta en:

```text
/usr/share/nginx/html/custom-icons
```

y queda accesible desde el navegador como:

```text
http://localhost:8080/custom-icons/mi-icono.svg
```

Para usarlo en un tipo de equipo, escribe en el campo **Icono**:

- `mi-icono.svg` (se resuelve como `custom-icons/mi-icono.svg`)
- `custom-icons/mi-icono.svg`
- `custom:mi-icono.svg`

También puedes usar una URL `https://...` si el icono está publicado externamente.

Si cambias `CUSTOM_ICONS_PATH` en `.env`, reinicia el contenedor web. Los iconos personalizados se montan con el compose avanzado:

```bash
docker compose -f docker-compose.yml -f docker-compose.advanced.yml up -d
```
