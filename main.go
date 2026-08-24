package main

import (
	"bufio"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// MODELO
// ---------------------------------------------------------------------------

// Libro representa un libro electrónico tal como lo ve la aplicación.
// Ojo: en la base de datos el autor y la categoría YA NO son texto dentro de
// la tabla libros, sino filas de las tablas `autores` y `categorias`.
// Aquí los guardamos como texto solo para mostrarlos con comodidad.
type Libro struct {
	ID         int64
	Titulo     string
	Anio       int
	ISBN       string // "" significa NULL en la base de datos
	Categoria  string // "" significa "sin categoría"
	Precio     float64
	Formato    string   // "" significa NULL
	Autores    []string // un libro puede tener varios (relación N:M)
	Creado     string
	Modificado string
}

// formatosValidos son los únicos valores que acepta la columna `formato`.
// La misma lista está replicada como CHECK dentro de la tabla: la aplicación
// valida para dar un mensaje amable, y la base de datos valida para que
// NADIE (ni DBeaver, ni otro programa) pueda meter un valor inválido.
var formatosValidos = []string{"PDF", "EPUB", "MOBI", "AZW3", "TXT"}

// lector se usa para leer lo que el usuario escribe por teclado.
var lector = bufio.NewReader(os.Stdin)

func main() {
	// Debe hacerse ANTES de abrir la conexión: el driver solo expone la
	// función a las conexiones creadas después de registrarla.
	if err := registrarFuncionesSQL(); err != nil {
		log.Fatalf("no se pudieron registrar las funciones SQL: %v", err)
	}

	db, err := abrirBD("libros.db")
	if err != nil {
		log.Fatalf("no se pudo abrir la base de datos: %v", err)
	}
	defer db.Close()

	if err := aplicarMigraciones(db); err != nil {
		log.Fatalf("no se pudieron aplicar las migraciones: %v", err)
	}

	// Modo web: en vez del menú de consola, levanta el servidor HTTP con la
	// misma base y las mismas funciones (ver web.go).
	//     ./gestor-libros -web         -> http://localhost:8090
	//     ./gestor-libros -web :9000   -> otro puerto
	if direccion, esWeb := direccionWeb(os.Args[1:]); esWeb {
		if err := servirWeb(db, direccion); err != nil {
			log.Fatalf("no se pudo levantar el servidor web: %v", err)
		}
		return
	}

	for {
		mostrarMenu()
		switch leerTexto("Elige una opción: ") {
		case "1":
			agregarLibro(db)
		case "2":
			listarLibros(db)
		case "3":
			menuBuscar(db)
		case "4":
			actualizarLibro(db)
		case "5":
			eliminarLibro(db)
		case "6":
			menuReportes(db)
		case "7":
			verAuditoria(db)
		case "8", "0":
			fmt.Println("\n¡Hasta luego!")
			return
		default:
			fmt.Println("\nOpción no válida. Intenta de nuevo.")
		}
	}
}

func mostrarMenu() {
	fmt.Println("\nGestión de Libros electrónicos")
	fmt.Println("---------------------------------------")
	fmt.Println("  1) Agregar libro")
	fmt.Println("  2) Listar catálogo")
	fmt.Println("  3) Buscar libros (ID, título o autor)")
	fmt.Println("  4) Actualizar libro")
	fmt.Println("  5) Eliminar libro")
	fmt.Println("  6) Reportes y estadísticas")
	fmt.Println("  7) Historial de cambios (auditoría)")
	fmt.Println("  8) Salir")
	fmt.Println("---------------------------------------")
}

// ---------------------------------------------------------------------------
// CONEXIÓN
// ---------------------------------------------------------------------------

// abrirBD abre el archivo SQLite dejando la conexión bien configurada.
func abrirBD(ruta string) (*sql.DB, error) {
	// Los PRAGMA en SQLite valen POR CONEXIÓN, no por base de datos.
	// Como database/sql mantiene un pool de conexiones, ejecutar
	// `db.Exec("PRAGMA foreign_keys=ON")` solo configuraría UNA conexión del
	// pool y las demás quedarían con las claves foráneas apagadas.
	// Por eso los pasamos en la cadena de conexión (_pragma=...): así el
	// driver los aplica a CADA conexión que abra.
	dsn := ruta +
		"?_pragma=foreign_keys(1)" + // hace que las FOREIGN KEY realmente se validen
		"&_pragma=busy_timeout(5000)" // espera 5 s si el archivo está bloqueado

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// Es una aplicación de consola de un solo usuario: con una conexión basta
	// y además evitamos cualquier bloqueo entre conexiones del pool.
	db.SetMaxOpenConns(1)

	// sql.Open no conecta de verdad; Ping fuerza la conexión para detectar
	// errores (archivo corrupto, permisos) aquí y no a mitad del programa.
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

// ---------------------------------------------------------------------------
// FUNCIONES SQL PROPIAS
// ---------------------------------------------------------------------------

// acentos traduce cada letra acentuada a su letra base.
var acentos = map[rune]rune{
	'á': 'a', 'à': 'a', 'ä': 'a', 'â': 'a', 'ã': 'a',
	'é': 'e', 'è': 'e', 'ë': 'e', 'ê': 'e',
	'í': 'i', 'ì': 'i', 'ï': 'i', 'î': 'i',
	'ó': 'o', 'ò': 'o', 'ö': 'o', 'ô': 'o', 'õ': 'o',
	'ú': 'u', 'ù': 'u', 'ü': 'u', 'û': 'u',
	'ñ': 'n', 'ç': 'c',
}

// sinAcentos pasa un texto a minúsculas y le quita las tildes.
//
// Hace falta porque el LIKE de SQLite solo ignora mayúsculas en el alfabeto
// ASCII: para el motor, 'Á' y 'á' son letras distintas y ninguna se parece a
// 'a'. Sin esto, buscar "garcia" jamás encontraría a "García Márquez".
func sinAcentos(s string) string {
	return strings.Map(func(r rune) rune {
		if base, ok := acentos[r]; ok {
			return base
		}
		return r
	}, strings.ToLower(s))
}

// registrarFuncionesSQL publica sinAcentos dentro de SQLite con el nombre
// `sin_acentos(texto)`, de modo que se pueda usar dentro de un SELECT igual
// que upper() o length(). SQLite invoca el código Go en cada fila.
//
// Se declara "determinista" (misma entrada -> misma salida) para que el motor
// pueda memorizar el resultado en lugar de recalcularlo.
func registrarFuncionesSQL() error {
	return sqlite.RegisterDeterministicScalarFunction("sin_acentos", 1,
		func(ctx *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			texto, ok := args[0].(string)
			if !ok {
				return args[0], nil // NULL o número: se devuelve tal cual
			}
			return sinAcentos(texto), nil
		})
}

// ---------------------------------------------------------------------------
// MIGRACIONES VERSIONADAS
// ---------------------------------------------------------------------------

// Cada migración se aplica UNA sola vez y en orden. El número de la última
// migración aplicada se guarda en el propio archivo .db mediante
// `PRAGMA user_version`, un entero que SQLite reserva para uso de la
// aplicación. Así el programa sabe, al arrancar, qué le falta por hacer,
// sin necesidad de una tabla extra.
var migraciones = []struct {
	version int
	nombre  string
	aplicar func(*sql.Tx) error
}{
	{1, "esquema relacional (autores, categorías, N:M, auditoría)", migracion1},
}

func aplicarMigraciones(db *sql.DB) error {
	var versionActual int
	if err := db.QueryRow("PRAGMA user_version").Scan(&versionActual); err != nil {
		return err
	}

	for _, m := range migraciones {
		if m.version <= versionActual {
			continue // ya se aplicó en una ejecución anterior
		}
		fmt.Printf("Aplicando migración %d: %s...\n", m.version, m.nombre)

		// TRANSACCIÓN: o se aplica la migración completa, o no se aplica nada.
		// Si algo falla a la mitad, el Rollback deja la base tal como estaba.
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if err := m.aplicar(tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("migración %d: %w", m.version, err)
		}
		// user_version también es transaccional: se guarda junto con los cambios.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		fmt.Printf("Migración %d aplicada.\n", m.version)
	}
	return nil
}

// migracion1 construye el esquema relacional completo.
//
// Si la base ya tenía la tabla `libros` vieja (con autor y categoría como
// texto), la reconstruye SIN perder datos. SQLite no permite cambiar el tipo
// ni las restricciones de una columna existente, así que se usa el
// procedimiento oficial: renombrar la tabla vieja, crear la nueva, copiar los
// datos y borrar la vieja.
func migracion1(tx *sql.Tx) error {
	teniaTablaVieja, err := existeColumna(tx, "libros", "autor")
	if err != nil {
		return err
	}

	if teniaTablaVieja {
		// Se renombra ANTES de crear libro_autor: desde SQLite 3.25 un RENAME
		// reescribe automáticamente las referencias de otras tablas, y eso
		// haría que la tabla puente apuntara a libros_old.
		if _, err := tx.Exec(`ALTER TABLE libros RENAME TO libros_old`); err != nil {
			return err
		}
	}

	sentencias := []string{
		// -------------------------------------------------------------------
		// Catálogos: cada autor y cada categoría existen UNA sola vez.
		// UNIQUE + COLLATE NOCASE impide que "Rothfuss" y "ROTHFUSS" se
		// guarden como dos autores distintos.
		// -------------------------------------------------------------------
		`CREATE TABLE autores (
			id     INTEGER PRIMARY KEY AUTOINCREMENT,
			nombre TEXT NOT NULL UNIQUE COLLATE NOCASE
			       CHECK (length(trim(nombre)) > 0)
		)`,
		`CREATE TABLE categorias (
			id     INTEGER PRIMARY KEY AUTOINCREMENT,
			nombre TEXT NOT NULL UNIQUE COLLATE NOCASE
			       CHECK (length(trim(nombre)) > 0)
		)`,

		// -------------------------------------------------------------------
		// Tabla principal, ahora con restricciones de integridad reales.
		// -------------------------------------------------------------------
		`CREATE TABLE libros (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			titulo       TEXT    NOT NULL CHECK (length(trim(titulo)) > 0),
			anio         INTEGER NOT NULL CHECK (anio BETWEEN 1450 AND 2100),
			-- UNIQUE evita ISBN repetidos. En SQLite UNIQUE sí admite varios
			-- NULL, por eso un libro sin ISBN guarda NULL (no cadena vacía).
			isbn         TEXT    UNIQUE CHECK (isbn IS NULL OR length(trim(isbn)) > 0),
			categoria_id INTEGER REFERENCES categorias(id) ON DELETE SET NULL,
			precio       REAL    NOT NULL DEFAULT 0 CHECK (precio >= 0),
			formato      TEXT    CHECK (formato IS NULL OR formato IN ('PDF','EPUB','MOBI','AZW3','TXT')),
			fecha_creacion     TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			fecha_modificacion TEXT
		)`,
	}

	// Copia de los datos antiguos a la estructura nueva.
	if teniaTablaVieja {
		sentencias = append(sentencias,
			// Un autor por cada nombre distinto que había en la tabla vieja.
			`INSERT OR IGNORE INTO autores (nombre)
			 SELECT DISTINCT trim(autor) FROM libros_old WHERE trim(autor) <> ''`,
			`INSERT OR IGNORE INTO categorias (nombre)
			 SELECT DISTINCT trim(categoria) FROM libros_old WHERE trim(categoria) <> ''`,

			// Se conservan los mismos IDs para no romper referencias externas.
			// NULLIF convierte las cadenas vacías en NULL, que es como se
			// representa correctamente "este dato no existe".
			`INSERT INTO libros (id, titulo, anio, isbn, categoria_id, precio, formato)
			 SELECT o.id,
			        trim(o.titulo),
			        o.anio,
			        NULLIF(trim(o.isbn), ''),
			        (SELECT c.id FROM categorias c WHERE c.nombre = trim(o.categoria)),
			        COALESCE(o.precio, 0),
			        CASE WHEN upper(trim(o.formato)) IN ('PDF','EPUB','MOBI','AZW3','TXT')
			             THEN upper(trim(o.formato)) ELSE NULL END
			 FROM libros_old o`,
		)
	}

	sentencias = append(sentencias,
		// -------------------------------------------------------------------
		// Relación N:M: un libro puede tener varios autores y un autor varios
		// libros. La clave primaria compuesta impide repetir el mismo par.
		// ON DELETE CASCADE: al borrar un libro se borran solas sus filas aquí.
		// -------------------------------------------------------------------
		`CREATE TABLE libro_autor (
			libro_id INTEGER NOT NULL REFERENCES libros(id)  ON DELETE CASCADE,
			autor_id INTEGER NOT NULL REFERENCES autores(id) ON DELETE CASCADE,
			orden    INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY (libro_id, autor_id)
		)`,
	)

	if teniaTablaVieja {
		sentencias = append(sentencias,
			`INSERT INTO libro_autor (libro_id, autor_id, orden)
			 SELECT o.id, a.id, 1
			 FROM libros_old o JOIN autores a ON a.nombre = trim(o.autor)
			 WHERE trim(o.autor) <> ''`,
			`DROP TABLE libros_old`,
		)
	}

	sentencias = append(sentencias,
		// -------------------------------------------------------------------
		// ÍNDICES: aceleran las búsquedas y los JOIN.
		// No hace falta indexar libro_autor.libro_id porque la clave primaria
		// compuesta (libro_id, autor_id) ya sirve de índice para ese lado.
		// -------------------------------------------------------------------
		`CREATE INDEX idx_libros_categoria   ON libros(categoria_id)`,
		`CREATE INDEX idx_libros_titulo      ON libros(titulo)`,
		`CREATE INDEX idx_libros_anio        ON libros(anio)`,
		`CREATE INDEX idx_libro_autor_autor  ON libro_autor(autor_id)`,

		// -------------------------------------------------------------------
		// AUDITORÍA: bitácora de todo lo que le pasa a la tabla libros.
		// -------------------------------------------------------------------
		`CREATE TABLE libros_auditoria (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			libro_id       INTEGER NOT NULL,
			operacion      TEXT    NOT NULL CHECK (operacion IN ('INSERT','UPDATE','DELETE')),
			campo          TEXT,
			valor_anterior TEXT,
			valor_nuevo    TEXT,
			fecha          TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE INDEX idx_auditoria_libro ON libros_auditoria(libro_id)`,

		// Alta de un libro.
		`CREATE TRIGGER tr_libros_insert AFTER INSERT ON libros
		 BEGIN
			INSERT INTO libros_auditoria (libro_id, operacion, valor_nuevo)
			VALUES (new.id, 'INSERT', new.titulo);
		 END`,

		// Baja de un libro. Se dispara también cuando el borrado viene de un
		// ON DELETE CASCADE, así que nada se pierde de la bitácora.
		`CREATE TRIGGER tr_libros_delete AFTER DELETE ON libros
		 BEGIN
			INSERT INTO libros_auditoria (libro_id, operacion, valor_anterior)
			VALUES (old.id, 'DELETE', old.titulo);
		 END`,

		// Sella la fecha de modificación sin que la aplicación tenga que
		// acordarse de hacerlo. El WHEN evita que el trigger se dispare a sí
		// mismo indefinidamente.
		`CREATE TRIGGER tr_libros_fecha_mod AFTER UPDATE ON libros
		 FOR EACH ROW WHEN new.fecha_modificacion IS old.fecha_modificacion
		 BEGIN
			UPDATE libros SET fecha_modificacion = datetime('now','localtime')
			WHERE id = new.id;
		 END`,

		// -------------------------------------------------------------------
		// VISTA: deja el JOIN de cuatro tablas escrito UNA sola vez.
		// El programa (y DBeaver) consultan v_catalogo como si fuera una tabla.
		// -------------------------------------------------------------------
		`CREATE VIEW v_catalogo AS
		 SELECT l.id                                        AS id,
		        l.titulo                                    AS titulo,
		        COALESCE(group_concat(a.nombre, ', '), '—') AS autores,
		        l.anio                                      AS anio,
		        COALESCE(l.isbn, '—')                       AS isbn,
		        COALESCE(c.nombre, '(sin categoría)')       AS categoria,
		        l.precio                                    AS precio,
		        COALESCE(l.formato, '—')                    AS formato
		 FROM libros l
		 LEFT JOIN categorias c  ON c.id  = l.categoria_id
		 LEFT JOIN libro_autor la ON la.libro_id = l.id
		 LEFT JOIN autores a      ON a.id  = la.autor_id
		 GROUP BY l.id`,
	)

	for _, s := range sentencias {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("%w\n--- SQL ---\n%s", err, s)
		}
	}

	// Los triggers de auditoría por campo se generan con un bucle porque los
	// siete son idénticos salvo el nombre de la columna.
	// `IS NOT` (en vez de `<>`) compara correctamente aunque haya NULL.
	for _, campo := range []string{"titulo", "anio", "isbn", "categoria_id", "precio", "formato"} {
		sql := fmt.Sprintf(`
			CREATE TRIGGER tr_libros_update_%[1]s
			AFTER UPDATE OF %[1]s ON libros
			FOR EACH ROW WHEN old.%[1]s IS NOT new.%[1]s
			BEGIN
				INSERT INTO libros_auditoria (libro_id, operacion, campo, valor_anterior, valor_nuevo)
				VALUES (new.id, 'UPDATE', '%[1]s', CAST(old.%[1]s AS TEXT), CAST(new.%[1]s AS TEXT));
			END`, campo)
		if _, err := tx.Exec(sql); err != nil {
			return fmt.Errorf("trigger de %s: %w", campo, err)
		}
	}
	return nil
}

// existeColumna indica si una tabla existe Y tiene la columna indicada.
func existeColumna(tx *sql.Tx, tabla, columna string) (bool, error) {
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", tabla))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	encontrada := false
	for rows.Next() {
		var (
			cid          int
			nombre, tipo string
			notNull, pk  int
			porDefecto   sql.NullString
		)
		if err := rows.Scan(&cid, &nombre, &tipo, &notNull, &porDefecto, &pk); err != nil {
			return false, err
		}
		if nombre == columna {
			encontrada = true
		}
	}
	return encontrada, rows.Err()
}

// ---------------------------------------------------------------------------
// CREATE
// ---------------------------------------------------------------------------

func agregarLibro(db *sql.DB) {
	fmt.Println("\n--- Agregar nuevo libro ---")
	titulo := leerTexto("Título: ")
	autores := leerLista("Autor(es), separados por coma: ")
	anio := leerEntero("Año: ")
	isbn := leerTexto("ISBN (Enter si no tiene): ")
	categoria := leerTexto("Categoría: ")
	precio := leerDecimal("Precio: ")
	formato := leerFormato()

	// TRANSACCIÓN: el libro y sus autores se guardan como una sola unidad.
	// Sin esto, un fallo al insertar el segundo autor dejaría un libro a
	// medias en la base de datos.
	tx, err := db.Begin()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	// Si algo sale mal y salimos antes del Commit, esto deshace todo.
	// Tras un Commit exitoso, Rollback no hace nada.
	defer tx.Rollback()

	categoriaID, err := obtenerOCrear(tx, "categorias", categoria)
	if err != nil {
		fmt.Printf("Error con la categoría: %s\n", describirError(err))
		return
	}

	res, err := tx.Exec(
		`INSERT INTO libros (titulo, anio, isbn, categoria_id, precio, formato)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		titulo, anio, textoONulo(isbn), categoriaID, precio, textoONulo(formato),
	)
	if err != nil {
		fmt.Printf("Error al agregar: %s\n", describirError(err))
		return
	}
	libroID, _ := res.LastInsertId()

	if err := guardarAutores(tx, libroID, autores); err != nil {
		fmt.Printf("Error con los autores: %s\n", describirError(err))
		return
	}

	if err := tx.Commit(); err != nil {
		fmt.Printf("Error al confirmar: %s\n", describirError(err))
		return
	}
	fmt.Printf("Libro agregado con ID %d.\n", libroID)
}

// obtenerOCrear devuelve el id de un autor/categoría, creándolo si aún no
// existe. Es el patrón "get or create" que evita duplicados en los catálogos.
// `tabla` nunca viene del usuario: siempre es una constante del programa.
func obtenerOCrear(tx *sql.Tx, tabla, nombre string) (sql.NullInt64, error) {
	nombre = strings.TrimSpace(nombre)
	if nombre == "" {
		return sql.NullInt64{}, nil // NULL: sin categoría
	}

	var id int64
	err := tx.QueryRow("SELECT id FROM "+tabla+" WHERE nombre = ?", nombre).Scan(&id)
	switch {
	case err == sql.ErrNoRows:
		res, err := tx.Exec("INSERT INTO "+tabla+" (nombre) VALUES (?)", nombre)
		if err != nil {
			return sql.NullInt64{}, err
		}
		id, _ = res.LastInsertId()
	case err != nil:
		return sql.NullInt64{}, err
	}
	return sql.NullInt64{Int64: id, Valid: true}, nil
}

// guardarAutores vincula un libro con su lista de autores en la tabla puente.
func guardarAutores(tx *sql.Tx, libroID int64, autores []string) error {
	for i, nombre := range autores {
		autorID, err := obtenerOCrear(tx, "autores", nombre)
		if err != nil {
			return err
		}
		if !autorID.Valid {
			continue
		}
		// OR IGNORE: si el usuario escribe el mismo autor dos veces, la clave
		// primaria compuesta lo rechazaría; así simplemente se omite.
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO libro_autor (libro_id, autor_id, orden) VALUES (?, ?, ?)`,
			libroID, autorID.Int64, i+1,
		); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// READ
// ---------------------------------------------------------------------------

// columnasCatalogo son las columnas que devuelve la vista, en el orden que
// espera imprimirCatalogo. Las comparten el listado y todas las búsquedas.
//
// Van calificadas con el alias `v` a propósito: la vista tiene una columna
// llamada `autores` y además existe una TABLA llamada `autores`. En las
// búsquedas que hacen JOIN con esa tabla, un `id` o un `autores` a secas sería
// ambiguo y SQLite rechazaría la consulta.
const columnasCatalogo = `v.id, v.titulo, v.autores, v.anio, v.isbn, v.categoria, v.precio, v.formato`

// listarLibros consulta la VISTA, no las tablas: el JOIN ya está resuelto ahí.
func listarLibros(db *sql.DB) {
	rows, err := db.Query(`SELECT ` + columnasCatalogo + ` FROM v_catalogo v ORDER BY v.id`)
	imprimirCatalogo(rows, err, "Catálogo", "(No hay libros registrados todavía.)")
}

// imprimirCatalogo dibuja la tabla de resultados. La usan el listado completo
// y las tres búsquedas, para que todas se vean igual.
func imprimirCatalogo(rows *sql.Rows, err error, titulo, mensajeVacio string) {
	if err != nil {
		fmt.Printf("Error en la consulta: %v\n", err)
		return
	}
	defer rows.Close()

	// Se arman primero todas las líneas para saber si hubo resultados antes
	// de dibujar la cabecera: así una búsqueda sin coincidencias muestra solo
	// el mensaje, sin una tabla vacía.
	var lineas []string
	for rows.Next() {
		var (
			id                                      int64
			titulo, autores, isbn, categoria, forma string
			anio                                    int
			precio                                  float64
		)
		if err := rows.Scan(&id, &titulo, &autores, &anio, &isbn, &categoria, &precio, &forma); err != nil {
			fmt.Printf("Error al leer fila: %v\n", err)
			return
		}
		lineas = append(lineas, fmt.Sprintf(
			"%-4d | %-26s | %-24s | %-4d | %-14s | %-14s | %8.2f | %-5s",
			id, recortar(titulo, 26), recortar(autores, 24), anio,
			recortar(isbn, 14), recortar(categoria, 14), precio, forma))
	}
	if err := rows.Err(); err != nil { // errores que ocurren a mitad del recorrido
		fmt.Printf("Error durante el recorrido: %v\n", err)
		return
	}

	fmt.Printf("\n--- %s ---\n", titulo)
	if len(lineas) == 0 {
		fmt.Println(mensajeVacio)
		return
	}
	fmt.Printf("%-4s | %-26s | %-24s | %-4s | %-14s | %-14s | %8s | %-5s\n",
		"ID", "TÍTULO", "AUTOR(ES)", "AÑO", "ISBN", "CATEGORÍA", "PRECIO", "FORM.")
	fmt.Println(strings.Repeat("-", 120))
	for _, l := range lineas {
		fmt.Println(l)
	}
	fmt.Printf("\n%d resultado(s).\n", len(lineas))
}

// ---------------------------------------------------------------------------
// BÚSQUEDAS
// ---------------------------------------------------------------------------

func menuBuscar(db *sql.DB) {
	for {
		fmt.Println("\n--- Buscar libros ---")
		fmt.Println("  1) Por ID (ficha completa)")
		fmt.Println("  2) Por título")
		fmt.Println("  3) Por autor")
		fmt.Println("  4) Por título o autor")
		fmt.Println("  5) Volver")

		switch leerTexto("Elige una opción: ") {
		case "1":
			buscarPorID(db)
		case "2":
			buscarPorTitulo(db)
		case "3":
			buscarPorAutor(db)
		case "4":
			buscarGeneral(db)
		case "5", "0":
			return
		default:
			fmt.Println("Opción no válida.")
		}
	}
}

// patronLike arma el patrón de búsqueda "contiene el texto".
//
// Los caracteres % y _ son comodines de LIKE, así que si el usuario los
// escribe hay que neutralizarlos: sin esto, buscar "100%" traería todo el
// catálogo. Se escapan con \ y en el SQL se declara ESCAPE '\'.
func patronLike(texto string) string {
	texto = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(texto)
	return "%" + texto + "%"
}

// buscarPorTitulo: LIKE sobre el título, ignorando mayúsculas y tildes.
func buscarPorTitulo(db *sql.DB) {
	texto := leerTexto("Título o parte del título: ")
	if texto == "" {
		fmt.Println("No escribiste nada.")
		return
	}
	rows, err := db.Query(`
		SELECT `+columnasCatalogo+`
		FROM v_catalogo v
		WHERE sin_acentos(v.titulo) LIKE sin_acentos(?) ESCAPE '\'
		ORDER BY v.titulo`, patronLike(texto))
	imprimirCatalogo(rows, err, "Resultados por título",
		"(Ningún libro coincide con ese título.)")
}

// buscarPorAutor: recorre la relación N:M para llegar del autor a sus libros.
// DISTINCT evita que un libro salga repetido si dos de sus coautores coinciden
// con el texto buscado (p. ej. buscar "a" en "Terry Pratchett, Neil Gaiman").
func buscarPorAutor(db *sql.DB) {
	texto := leerTexto("Nombre o parte del nombre del autor: ")
	if texto == "" {
		fmt.Println("No escribiste nada.")
		return
	}
	rows, err := db.Query(`
		SELECT DISTINCT `+columnasCatalogo+`
		FROM v_catalogo v
		JOIN libro_autor la ON la.libro_id = v.id
		JOIN autores     a  ON a.id        = la.autor_id
		WHERE sin_acentos(a.nombre) LIKE sin_acentos(?) ESCAPE '\'
		ORDER BY v.titulo`, patronLike(texto))
	imprimirCatalogo(rows, err, "Resultados por autor",
		"(Ningún autor coincide con ese nombre.)")
}

// buscarGeneral: una sola caja de búsqueda que mira título Y autores.
// La condición del autor va como subconsulta con IN, así no hacen falta JOIN
// ni DISTINCT: basta con que exista al menos un autor que coincida.
func buscarGeneral(db *sql.DB) {
	texto := leerTexto("Texto a buscar (título o autor): ")
	if texto == "" {
		fmt.Println("No escribiste nada.")
		return
	}
	patron := patronLike(texto)
	rows, err := db.Query(`
		SELECT `+columnasCatalogo+`
		FROM v_catalogo v
		WHERE sin_acentos(v.titulo) LIKE sin_acentos(?) ESCAPE '\'
		   OR v.id IN (
				SELECT la.libro_id
				FROM libro_autor la
				JOIN autores a ON a.id = la.autor_id
				WHERE sin_acentos(a.nombre) LIKE sin_acentos(?) ESCAPE '\'
		      )
		ORDER BY v.titulo`, patron, patron)
	imprimirCatalogo(rows, err, "Resultados", "(Sin coincidencias en títulos ni autores.)")
}

func buscarPorID(db *sql.DB) {
	id := leerEntero("ID del libro: ")

	l, err := obtenerLibro(db, int64(id))
	if err == sql.ErrNoRows {
		fmt.Printf("No existe ningún libro con ID %d.\n", id)
		return
	}
	if err != nil {
		fmt.Printf("Error al buscar: %v\n", err)
		return
	}

	fmt.Println("\nDetalle del libro")
	fmt.Printf("   ID:          %d\n", l.ID)
	fmt.Printf("   Título:      %s\n", l.Titulo)
	fmt.Printf("   Autor(es):   %s\n", textoOGuion(strings.Join(l.Autores, ", ")))
	fmt.Printf("   Año:         %d\n", l.Anio)
	fmt.Printf("   ISBN:        %s\n", textoOGuion(l.ISBN))
	fmt.Printf("   Categoría:   %s\n", textoOGuion(l.Categoria))
	fmt.Printf("   Precio:      $%.2f\n", l.Precio)
	fmt.Printf("   Formato:     %s\n", textoOGuion(l.Formato))
	fmt.Printf("   Registrado:  %s\n", l.Creado)
	fmt.Printf("   Modificado:  %s\n", textoOGuion(l.Modificado))
}

// obtenerLibro trae un libro con su categoría (JOIN) y sus autores (2ª consulta).
func obtenerLibro(db *sql.DB, id int64) (Libro, error) {
	var (
		l                        Libro
		isbn, categoria, formato sql.NullString
		modificado               sql.NullString
	)
	err := db.QueryRow(
		`SELECT l.id, l.titulo, l.anio, l.isbn, c.nombre, l.precio, l.formato,
		        l.fecha_creacion, l.fecha_modificacion
		 FROM libros l
		 LEFT JOIN categorias c ON c.id = l.categoria_id
		 WHERE l.id = ?`, id,
	).Scan(&l.ID, &l.Titulo, &l.Anio, &isbn, &categoria, &l.Precio, &formato,
		&l.Creado, &modificado)
	if err != nil {
		return l, err
	}
	// Las columnas que admiten NULL se leen con sql.NullString porque un NULL
	// no se puede guardar en un string de Go.
	l.ISBN, l.Categoria, l.Formato, l.Modificado =
		isbn.String, categoria.String, formato.String, modificado.String

	rows, err := db.Query(
		`SELECT a.nombre
		 FROM libro_autor la JOIN autores a ON a.id = la.autor_id
		 WHERE la.libro_id = ? ORDER BY la.orden, a.nombre`, id)
	if err != nil {
		return l, err
	}
	defer rows.Close()
	for rows.Next() {
		var nombre string
		if err := rows.Scan(&nombre); err != nil {
			return l, err
		}
		l.Autores = append(l.Autores, nombre)
	}
	return l, rows.Err()
}

// ---------------------------------------------------------------------------
// UPDATE
// ---------------------------------------------------------------------------

func actualizarLibro(db *sql.DB) {
	fmt.Println("\n--- Actualizar libro ---")
	id := leerEntero("ID del libro a actualizar: ")

	actual, err := obtenerLibro(db, int64(id))
	if err == sql.ErrNoRows {
		fmt.Printf("No existe ningún libro con ID %d.\n", id)
		return
	}
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Datos actuales: %q — %s (%d) | ISBN %s | %s | $%.2f | %s\n",
		actual.Titulo, textoOGuion(strings.Join(actual.Autores, ", ")), actual.Anio,
		textoOGuion(actual.ISBN), textoOGuion(actual.Categoria), actual.Precio,
		textoOGuion(actual.Formato))
	fmt.Println("(Deja el campo vacío y presiona Enter para mantener el valor actual.)")

	nuevo := actual
	cambiarAutores := false

	if v := leerTexto("Nuevo título: "); v != "" {
		nuevo.Titulo = v
	}
	if v := leerTexto("Nuevo(s) autor(es), separados por coma: "); v != "" {
		nuevo.Autores = separarPorComa(v)
		cambiarAutores = true
	}
	if v := leerTexto("Nuevo año: "); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			nuevo.Anio = n
		} else {
			fmt.Println("Año inválido, se mantiene el actual.")
		}
	}
	if v := leerTexto("Nuevo ISBN: "); v != "" {
		nuevo.ISBN = v
	}
	if v := leerTexto("Nueva categoría: "); v != "" {
		nuevo.Categoria = v
	}
	if v := leerTexto("Nuevo precio: "); v != "" {
		if p, err := strconv.ParseFloat(strings.Replace(v, ",", ".", 1), 64); err == nil {
			nuevo.Precio = p
		} else {
			fmt.Println("Precio inválido, se mantiene el actual.")
		}
	}
	if v := leerTexto("Nuevo formato (" + strings.Join(formatosValidos, "/") + "): "); v != "" {
		nuevo.Formato = strings.ToUpper(v)
	}

	tx, err := db.Begin()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer tx.Rollback()

	categoriaID, err := obtenerOCrear(tx, "categorias", nuevo.Categoria)
	if err != nil {
		fmt.Printf("Error con la categoría: %s\n", describirError(err))
		return
	}

	if _, err := tx.Exec(
		`UPDATE libros
		 SET titulo = ?, anio = ?, isbn = ?, categoria_id = ?, precio = ?, formato = ?
		 WHERE id = ?`,
		nuevo.Titulo, nuevo.Anio, textoONulo(nuevo.ISBN), categoriaID,
		nuevo.Precio, textoONulo(nuevo.Formato), id,
	); err != nil {
		fmt.Printf("Error al actualizar: %s\n", describirError(err))
		return
	}

	if cambiarAutores {
		// Se reemplaza la lista completa: primero se borran los vínculos
		// viejos (no los autores) y luego se crean los nuevos.
		if _, err := tx.Exec(`DELETE FROM libro_autor WHERE libro_id = ?`, id); err != nil {
			fmt.Printf("Error al limpiar autores: %v\n", err)
			return
		}
		if err := guardarAutores(tx, int64(id), nuevo.Autores); err != nil {
			fmt.Printf("Error con los autores: %s\n", describirError(err))
			return
		}
	}

	if err := tx.Commit(); err != nil {
		fmt.Printf("Error al confirmar: %s\n", describirError(err))
		return
	}
	fmt.Println("Libro actualizado correctamente.")
}

// ---------------------------------------------------------------------------
// DELETE
// ---------------------------------------------------------------------------

func eliminarLibro(db *sql.DB) {
	fmt.Println("\n--- Eliminar libro ---")
	id := leerEntero("ID del libro a eliminar: ")

	// No hace falta borrar a mano las filas de libro_autor: la FOREIGN KEY
	// tiene ON DELETE CASCADE y la base de datos las elimina sola.
	// Los autores y la categoría NO se borran: pueden pertenecer a otros libros.
	res, err := db.Exec("DELETE FROM libros WHERE id = ?", id)
	if err != nil {
		fmt.Printf("Error al eliminar: %s\n", describirError(err))
		return
	}
	filas, _ := res.RowsAffected()
	if filas == 0 {
		fmt.Printf("No existe ningún libro con ID %d.\n", id)
		return
	}
	fmt.Printf("Libro con ID %d eliminado (queda registrado en la auditoría).\n", id)
}

// ---------------------------------------------------------------------------
// REPORTES (consultas de agregación)
// ---------------------------------------------------------------------------

func menuReportes(db *sql.DB) {
	for {
		fmt.Println("\n--- Reportes y estadísticas ---")
		fmt.Println("  1) Resumen general")
		fmt.Println("  2) Libros por categoría")
		fmt.Println("  3) Autores más publicados")
		fmt.Println("  4) Libros por formato")
		fmt.Println("  5) Los 5 libros más caros")
		fmt.Println("  6) Plan de ejecución (¿se usan los índices?)")
		fmt.Println("  7) Volver")

		switch leerTexto("Elige una opción: ") {
		case "1":
			reporteResumen(db)
		case "2":
			reportePorCategoria(db)
		case "3":
			reporteAutores(db)
		case "4":
			reportePorFormato(db)
		case "5":
			reporteMasCaros(db)
		case "6":
			reportePlanEjecucion(db)
		case "7", "0":
			return
		default:
			fmt.Println("Opción no válida.")
		}
	}
}

// reporteResumen usa las funciones de agregación sobre TODA la tabla.
func reporteResumen(db *sql.DB) {
	var (
		total, autores, categorias int
		suma, promedio             sql.NullFloat64
		minPrecio, maxPrecio       sql.NullFloat64
		minAnio, maxAnio           sql.NullInt64
	)
	err := db.QueryRow(`
		SELECT COUNT(*), SUM(precio), AVG(precio), MIN(precio), MAX(precio),
		       MIN(anio), MAX(anio),
		       (SELECT COUNT(*) FROM autores),
		       (SELECT COUNT(*) FROM categorias)
		FROM libros`).Scan(&total, &suma, &promedio, &minPrecio, &maxPrecio,
		&minAnio, &maxAnio, &autores, &categorias)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Println("\nResumen general")
	fmt.Printf("   Libros registrados: %d\n", total)
	fmt.Printf("   Autores distintos:  %d\n", autores)
	fmt.Printf("   Categorías:         %d\n", categorias)
	if total == 0 {
		return
	}
	fmt.Printf("   Valor del catálogo: $%.2f\n", suma.Float64)
	fmt.Printf("   Precio promedio:    $%.2f\n", promedio.Float64)
	fmt.Printf("   Precio mín / máx:   $%.2f / $%.2f\n", minPrecio.Float64, maxPrecio.Float64)
	fmt.Printf("   Año más antiguo:    %d\n", minAnio.Int64)
	fmt.Printf("   Año más reciente:   %d\n", maxAnio.Int64)
}

// reportePorCategoria: GROUP BY sobre un LEFT JOIN.
func reportePorCategoria(db *sql.DB) {
	rows, err := db.Query(`
		SELECT COALESCE(c.nombre, '(sin categoría)') AS categoria,
		       COUNT(*)          AS cantidad,
		       ROUND(AVG(l.precio), 2) AS promedio,
		       ROUND(SUM(l.precio), 2) AS total
		FROM libros l
		LEFT JOIN categorias c ON c.id = l.categoria_id
		GROUP BY l.categoria_id
		ORDER BY cantidad DESC, categoria`)
	imprimirTabla(rows, err, "Libros por categoría",
		[]string{"CATEGORÍA", "CANT.", "PROM.", "TOTAL"})
}

// reporteAutores: JOIN + GROUP BY + HAVING + ORDER BY + LIMIT.
// HAVING filtra DESPUÉS de agrupar (WHERE filtra antes, sobre filas sueltas).
func reporteAutores(db *sql.DB) {
	minimo := leerEntero("Mostrar autores con al menos N libros (1 = todos): ")
	rows, err := db.Query(`
		SELECT a.nombre, COUNT(la.libro_id) AS cantidad
		FROM autores a
		JOIN libro_autor la ON la.autor_id = a.id
		GROUP BY a.id
		HAVING COUNT(la.libro_id) >= ?
		ORDER BY cantidad DESC, a.nombre
		LIMIT 10`, minimo)
	imprimirTabla(rows, err, "Autores más publicados", []string{"AUTOR", "LIBROS"})
}

func reportePorFormato(db *sql.DB) {
	rows, err := db.Query(`
		SELECT COALESCE(formato, '(sin formato)') AS formato,
		       COUNT(*) AS cantidad,
		       ROUND(AVG(precio), 2) AS promedio
		FROM libros
		GROUP BY formato
		ORDER BY cantidad DESC`)
	imprimirTabla(rows, err, "Libros por formato", []string{"FORMATO", "CANT.", "PROM."})
}

func reporteMasCaros(db *sql.DB) {
	rows, err := db.Query(`
		SELECT titulo, autores, precio
		FROM v_catalogo
		ORDER BY precio DESC, titulo
		LIMIT 5`)
	imprimirTabla(rows, err, "Los 5 libros más caros", []string{"TÍTULO", "AUTOR(ES)", "PRECIO"})
}

// reportePlanEjecucion muestra cómo SQLite piensa resolver una consulta.
// Sirve para comprobar que los índices creados realmente se están usando:
// si aparece "SCAN libros" recorre toda la tabla; si aparece
// "SEARCH libros USING INDEX ..." está aprovechando un índice.
func reportePlanEjecucion(db *sql.DB) {
	consultas := map[string]string{
		"Buscar por año (usa idx_libros_anio)":     "SELECT * FROM libros WHERE anio = 2007",
		"Buscar por precio (no hay índice)":        "SELECT * FROM libros WHERE precio > 10",
		"Libros de un autor (usa la tabla puente)": "SELECT l.titulo FROM libros l JOIN libro_autor la ON la.libro_id = l.id WHERE la.autor_id = 1",
	}
	for titulo, consulta := range consultas {
		fmt.Printf("\n%s\n   %s\n", titulo, consulta)
		rows, err := db.Query("EXPLAIN QUERY PLAN " + consulta)
		if err != nil {
			fmt.Printf("   Error: %v\n", err)
			continue
		}
		for rows.Next() {
			var a, b, c int
			var detalle string
			if err := rows.Scan(&a, &b, &c, &detalle); err != nil {
				break
			}
			fmt.Printf("   -> %s\n", detalle)
		}
		rows.Close()
	}
}

// ---------------------------------------------------------------------------
// AUDITORÍA
// ---------------------------------------------------------------------------

// verAuditoria muestra la bitácora que llenan los triggers. La aplicación
// nunca escribe en esta tabla: lo hace la base de datos por su cuenta.
func verAuditoria(db *sql.DB) {
	fmt.Println("\n--- Historial de cambios ---")
	filtro := leerTexto("ID de libro (Enter = todos): ")

	consulta := `SELECT fecha, libro_id, operacion,
	                    COALESCE(campo, '—'),
	                    COALESCE(valor_anterior, '—'),
	                    COALESCE(valor_nuevo, '—')
	             FROM libros_auditoria`
	args := []any{}
	if filtro != "" {
		consulta += " WHERE libro_id = ?"
		args = append(args, filtro)
	}
	consulta += " ORDER BY id DESC LIMIT 30"

	rows, err := db.Query(consulta, args...)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer rows.Close()

	fmt.Printf("\n%-19s | %-5s | %-9s | %-13s | %-18s | %-18s\n",
		"FECHA", "LIBRO", "OPERACIÓN", "CAMPO", "ANTES", "DESPUÉS")
	fmt.Println(strings.Repeat("-", 100))

	hubo := false
	for rows.Next() {
		var fecha, operacion, campo, antes, despues string
		var libroID int64
		if err := rows.Scan(&fecha, &libroID, &operacion, &campo, &antes, &despues); err != nil {
			fmt.Printf("Error al leer fila: %v\n", err)
			return
		}
		fmt.Printf("%-19s | %-5d | %-9s | %-13s | %-18s | %-18s\n",
			fecha, libroID, operacion, campo,
			recortar(antes, 18), recortar(despues, 18))
		hubo = true
	}
	if err := rows.Err(); err != nil {
		fmt.Printf("Error durante el recorrido: %v\n", err)
		return
	}
	if !hubo {
		fmt.Println("(Sin movimientos registrados.)")
	}
}

// ---------------------------------------------------------------------------
// UTILIDADES
// ---------------------------------------------------------------------------

// imprimirTabla vuelca cualquier resultado en columnas, sin saber de antemano
// cuántas ni de qué tipo son. Evita repetir el mismo bucle en cada reporte.
func imprimirTabla(rows *sql.Rows, err error, titulo string, cabeceras []string) {
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer rows.Close()

	fmt.Printf("\n%s\n", titulo)
	for i, c := range cabeceras {
		if i == 0 {
			fmt.Printf("%-30s", c)
		} else {
			fmt.Printf(" | %12s", c)
		}
	}
	fmt.Println()
	fmt.Println(strings.Repeat("-", 30+len(cabeceras)*15))

	hubo := false
	for rows.Next() {
		// Se leen todas las columnas como texto (sql.NullString convierte los
		// NULL y los números a su representación textual sin fallar).
		valores := make([]any, len(cabeceras))
		destino := make([]sql.NullString, len(cabeceras))
		for i := range destino {
			valores[i] = &destino[i]
		}
		if err := rows.Scan(valores...); err != nil {
			fmt.Printf("Error al leer fila: %v\n", err)
			return
		}
		for i, v := range destino {
			texto := v.String
			if !v.Valid {
				texto = "—"
			}
			if i == 0 {
				fmt.Printf("%-30s", recortar(texto, 30))
			} else {
				fmt.Printf(" | %12s", recortar(texto, 12))
			}
		}
		fmt.Println()
		hubo = true
	}
	if err := rows.Err(); err != nil {
		fmt.Printf("Error durante el recorrido: %v\n", err)
		return
	}
	if !hubo {
		fmt.Println("(Sin datos.)")
	}
}

// describirError traduce los errores de restricción de SQLite a un mensaje
// entendible. La base de datos rechaza el dato; aquí solo lo explicamos.
func describirError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed: libros.isbn"):
		return "ya existe otro libro con ese ISBN (la columna es UNIQUE)."
	case strings.Contains(msg, "UNIQUE constraint failed"):
		return "ese valor ya existe y debe ser único."
	case strings.Contains(msg, "CHECK constraint failed"):
		return "el dato no cumple una regla de la tabla (revisa año 1450-2100, precio >= 0, formato válido y título no vacío)."
	case strings.Contains(msg, "FOREIGN KEY constraint failed"):
		return "se hace referencia a un registro que no existe."
	case strings.Contains(msg, "NOT NULL constraint failed"):
		return "falta un dato obligatorio."
	}
	return msg
}

// textoONulo convierte "" en NULL para que la base de datos guarde ausencia de
// dato en lugar de una cadena vacía (importante para que UNIQUE funcione).
func textoONulo(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}

func textoOGuion(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// recortar corta un texto largo para que no rompa la alineación de la tabla.
func recortar(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func separarPorComa(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// ENTRADA POR TECLADO
// ---------------------------------------------------------------------------

func leerTexto(mensaje string) string {
	fmt.Print(mensaje)
	texto, _ := lector.ReadString('\n')
	return strings.TrimSpace(texto)
}

func leerLista(mensaje string) []string {
	return separarPorComa(leerTexto(mensaje))
}

func leerEntero(mensaje string) int {
	for {
		numero, err := strconv.Atoi(leerTexto(mensaje))
		if err != nil {
			fmt.Println("Ingresa un número válido.")
			continue
		}
		return numero
	}
}

func leerDecimal(mensaje string) float64 {
	for {
		texto := strings.Replace(leerTexto(mensaje), ",", ".", 1)
		numero, err := strconv.ParseFloat(texto, 64)
		if err != nil {
			fmt.Println("Ingresa un precio válido (ej: 12.50).")
			continue
		}
		return numero
	}
}

// leerFormato solo acepta uno de los formatos permitidos (o vacío).
func leerFormato() string {
	opciones := strings.Join(formatosValidos, "/")
	for {
		v := strings.ToUpper(leerTexto("Formato (" + opciones + ", Enter para omitir): "))
		if v == "" {
			return ""
		}
		for _, f := range formatosValidos {
			if v == f {
				return v
			}
		}
		fmt.Printf("Formato no válido. Usa uno de: %s\n", opciones)
	}
}

// direccionWeb decide si hay que arrancar en modo web y en qué dirección.
// Acepta:  -web | --web | web   y, opcionalmente, el puerto a continuación
// (":9000" o simplemente "9000").
func direccionWeb(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	switch args[0] {
	case "-web", "--web", "web":
	default:
		return "", false
	}

	direccion := ":8090"
	if len(args) > 1 && args[1] != "" {
		direccion = args[1]
		if !strings.Contains(direccion, ":") {
			direccion = ":" + direccion
		}
	}
	return direccion, true
}
