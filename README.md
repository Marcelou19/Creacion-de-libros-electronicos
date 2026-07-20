# 📚 Creación de Libros Electrónicos

Sistema de gestión de libros electrónicos desarrollado en **Go (Golang)** con base de datos
**SQLite**. Ofrece un menú interactivo por consola para realizar las operaciones **CRUD**
(Crear, Leer, Actualizar y Eliminar) sobre un catálogo de libros.

> Proyecto de la materia **Base de Datos (DBS)** — Unidad 3.

---

## ✨ Características

- Menú interactivo por consola (en español).
- **CRUD completo** de libros: agregar, listar, buscar, actualizar y eliminar.
- Base de datos **SQLite** en un solo archivo (`libros.db`), fácil de abrir en DBeaver.
- **Migración automática** del esquema: si la base es antigua, agrega las columnas que
  falten sin perder los datos.
- Driver **Go puro** (`modernc.org/sqlite`), no requiere compilador C ni CGO.

---

## 🗄️ Estructura de la tabla `libros`

| Columna     | Tipo    | Descripción                         |
|-------------|---------|-------------------------------------|
| `id`        | INTEGER | Clave primaria (autoincremental)    |
| `titulo`    | TEXT    | Título del libro                    |
| `autor`     | TEXT    | Autor                               |
| `anio`      | INTEGER | Año de publicación                  |
| `isbn`      | TEXT    | Código ISBN                         |
| `categoria` | TEXT    | Género o categoría                  |
| `precio`    | REAL    | Precio (decimal)                    |
| `formato`   | TEXT    | Formato: PDF, EPUB, MOBI, etc.      |

---

## 📋 Requisitos

- **Go 1.21 o superior** (desarrollado con Go 1.26).
- Conexión a internet la primera vez, para descargar la dependencia.

Verifica que tienes Go instalado:

```bash
go version
```

---

## 🚀 Instalación y ejecución

1. Clona el repositorio:

   ```bash
   git clone https://github.com/Marcelou19/Creacion-de-libros-electronicos.git
   cd Creacion-de-libros-electronicos
   ```

2. Descarga las dependencias:

   ```bash
   go mod tidy
   ```

3. Ejecuta el programa:

   ```bash
   go run .
   ```

Al iniciar por primera vez se crea automáticamente el archivo `libros.db`.

---

## 🕹️ Uso del menú

Al ejecutar el programa aparece este menú:

```
=======================================
   📚 GESTOR DE LIBROS ELECTRÓNICOS
=======================================
  1) Agregar libro
  2) Listar todos los libros
  3) Buscar libro por ID
  4) Actualizar libro
  5) Eliminar libro
  6) Salir
---------------------------------------
```

| Opción | Operación CRUD | Descripción                                              |
|--------|----------------|----------------------------------------------------------|
| **1**  | **C**reate     | Pide los datos y agrega un libro nuevo.                   |
| **2**  | **R**ead       | Muestra todos los libros en formato de tabla.            |
| **3**  | **R**ead       | Muestra la ficha detallada de un libro por su ID.        |
| **4**  | **U**pdate     | Actualiza un libro (Enter en un campo lo deja igual).    |
| **5**  | **D**elete     | Elimina un libro por su ID.                              |
| **6**  | —              | Cierra el programa.                                      |

> 💡 El **precio** acepta coma o punto como separador decimal (`19,99` o `19.99`).

---

## 🔌 Ver los datos en DBeaver

El archivo `libros.db` es una base **SQLite** estándar y puede abrirse en DBeaver:

1. **Base de datos → Nueva conexión → SQLite**.
2. En **Ruta (Path)**, selecciona el archivo `libros.db`.
3. Si lo pide, **descarga el controlador** SQLite.
4. **Probar conexión → Finalizar**.
5. Despliega `libros.db → Tablas → libros` y abre la pestaña **Datos**.

---

## 📁 Estructura del proyecto

```
Creacion-de-libros-electronicos/
├── main.go       # Programa principal: menú + operaciones CRUD
├── go.mod        # Definición del módulo y dependencias
├── go.sum        # Sumas de verificación de las dependencias
├── .gitignore    # Ignora libros.db, binarios, etc.
└── README.md     # Este archivo
```

> El archivo `libros.db` **no** se versiona en Git (está en `.gitignore`); se genera solo
> al ejecutar el programa.

---

## 🛠️ Tecnologías

- [Go](https://go.dev/) — lenguaje de programación.
- [SQLite](https://www.sqlite.org/) — motor de base de datos.
- [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) — driver SQLite en Go puro.
- [DBeaver](https://dbeaver.io/) — cliente para administrar la base de datos.
