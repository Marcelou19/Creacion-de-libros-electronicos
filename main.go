package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

// Libro representa un libro electrónico en la base de datos.
type Libro struct {
	ID        int64
	Titulo    string
	Autor     string
	Anio      int
	ISBN      string
	Categoria string
	Precio    float64
	Formato   string // PDF, EPUB, MOBI, etc.
}

// lector se usa para leer lo que el usuario escribe por teclado.
var lector = bufio.NewReader(os.Stdin)

func main() {
	// Abre (o crea) el archivo de base de datos SQLite.
	// Este es el mismo archivo "libros.db" que abres en DBeaver.
	db, err := sql.Open("sqlite", "libros.db")
	if err != nil {
		log.Fatalf("no se pudo abrir la base de datos: %v", err)
	}
	defer db.Close()

	if err := crearTabla(db); err != nil {
		log.Fatalf("no se pudo crear la tabla: %v", err)
	}
	if err := migrarTabla(db); err != nil {
		log.Fatalf("no se pudo migrar la tabla: %v", err)
	}

	// Bucle principal del menú.
	for {
		mostrarMenu()
		opcion := leerTexto("Elige una opción: ")

		switch opcion {
		case "1":
			agregarLibro(db)
		case "2":
			listarLibros(db)
		case "3":
			buscarLibro(db)
		case "4":
			actualizarLibro(db)
		case "5":
			eliminarLibro(db)
		case "6", "0":
			fmt.Println("\n¡Hasta luego! 👋")
			return
		default:
			fmt.Println("\n⚠️  Opción no válida. Intenta de nuevo.")
		}
	}
}

// ---------------------------------------------------------------------------
// MENÚ
// ---------------------------------------------------------------------------

func mostrarMenu() {
	fmt.Println("\n=======================================")
	fmt.Println("   📚 GESTOR DE LIBROS ELECTRÓNICOS")
	fmt.Println("=======================================")
	fmt.Println("  1) Agregar libro")
	fmt.Println("  2) Listar todos los libros")
	fmt.Println("  3) Buscar libro por ID")
	fmt.Println("  4) Actualizar libro")
	fmt.Println("  5) Eliminar libro")
	fmt.Println("  6) Salir")
	fmt.Println("---------------------------------------")
}

// ---------------------------------------------------------------------------
// OPERACIONES CRUD (Create, Read, Update, Delete)
// ---------------------------------------------------------------------------

// agregarLibro (CREATE): pide los datos y guarda un libro nuevo.
func agregarLibro(db *sql.DB) {
	fmt.Println("\n--- Agregar nuevo libro ---")
	titulo := leerTexto("Título: ")
	autor := leerTexto("Autor: ")
	anio := leerEntero("Año: ")
	isbn := leerTexto("ISBN: ")
	categoria := leerTexto("Categoría: ")
	precio := leerDecimal("Precio: ")
	formato := leerTexto("Formato (PDF/EPUB/MOBI): ")

	res, err := db.Exec(
		`INSERT INTO libros (titulo, autor, anio, isbn, categoria, precio, formato)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		titulo, autor, anio, isbn, categoria, precio, formato,
	)
	if err != nil {
		fmt.Printf("❌ Error al agregar: %v\n", err)
		return
	}
	id, _ := res.LastInsertId()
	fmt.Printf("✅ Libro agregado con ID %d.\n", id)
}

// listarLibros (READ): muestra todos los libros en una tabla.
func listarLibros(db *sql.DB) {
	rows, err := db.Query(
		`SELECT id, titulo, autor, anio, isbn, categoria, precio, formato
		 FROM libros ORDER BY id`)
	if err != nil {
		fmt.Printf("❌ Error al listar: %v\n", err)
		return
	}
	defer rows.Close()

	fmt.Println("\n--- Lista de libros ---")
	fmt.Printf("%-4s | %-24s | %-20s | %-4s | %-13s | %-12s | %-8s | %-6s\n",
		"ID", "TÍTULO", "AUTOR", "AÑO", "ISBN", "CATEGORÍA", "PRECIO", "FORM.")
	fmt.Println(strings.Repeat("-", 110))

	hubo := false
	for rows.Next() {
		var l Libro
		if err := rows.Scan(&l.ID, &l.Titulo, &l.Autor, &l.Anio,
			&l.ISBN, &l.Categoria, &l.Precio, &l.Formato); err != nil {
			fmt.Printf("❌ Error al leer fila: %v\n", err)
			return
		}
		fmt.Printf("%-4d | %-24s | %-20s | %-4d | %-13s | %-12s | %8.2f | %-6s\n",
			l.ID, l.Titulo, l.Autor, l.Anio, l.ISBN, l.Categoria, l.Precio, l.Formato)
		hubo = true
	}
	if !hubo {
		fmt.Println("(No hay libros registrados todavía.)")
	}
}

// buscarLibro (READ): muestra un libro por su ID.
func buscarLibro(db *sql.DB) {
	fmt.Println("\n--- Buscar libro ---")
	id := leerEntero("ID del libro: ")

	l, err := obtenerLibro(db, int64(id))
	if err == sql.ErrNoRows {
		fmt.Printf("⚠️  No existe ningún libro con ID %d.\n", id)
		return
	}
	if err != nil {
		fmt.Printf("❌ Error al buscar: %v\n", err)
		return
	}
	fmt.Println("\n📖 Detalle del libro")
	fmt.Printf("   ID:        %d\n", l.ID)
	fmt.Printf("   Título:    %s\n", l.Titulo)
	fmt.Printf("   Autor:     %s\n", l.Autor)
	fmt.Printf("   Año:       %d\n", l.Anio)
	fmt.Printf("   ISBN:      %s\n", l.ISBN)
	fmt.Printf("   Categoría: %s\n", l.Categoria)
	fmt.Printf("   Precio:    $%.2f\n", l.Precio)
	fmt.Printf("   Formato:   %s\n", l.Formato)
}

// actualizarLibro (UPDATE): cambia los datos de un libro existente.
func actualizarLibro(db *sql.DB) {
	fmt.Println("\n--- Actualizar libro ---")
	id := leerEntero("ID del libro a actualizar: ")

	// Primero verificamos que exista y mostramos sus datos actuales.
	actual, err := obtenerLibro(db, int64(id))
	if err == sql.ErrNoRows {
		fmt.Printf("⚠️  No existe ningún libro con ID %d.\n", id)
		return
	}
	if err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		return
	}

	fmt.Printf("Datos actuales: %q — %s (%d) | ISBN %s | %s | $%.2f | %s\n",
		actual.Titulo, actual.Autor, actual.Anio,
		actual.ISBN, actual.Categoria, actual.Precio, actual.Formato)
	fmt.Println("(Deja el campo vacío y presiona Enter para mantener el valor actual.)")

	nuevo := actual // partimos de los valores actuales

	if v := leerTexto("Nuevo título: "); v != "" {
		nuevo.Titulo = v
	}
	if v := leerTexto("Nuevo autor: "); v != "" {
		nuevo.Autor = v
	}
	if v := leerTexto("Nuevo año: "); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			nuevo.Anio = n
		} else {
			fmt.Println("⚠️  Año inválido, se mantiene el actual.")
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
			fmt.Println("⚠️  Precio inválido, se mantiene el actual.")
		}
	}
	if v := leerTexto("Nuevo formato (PDF/EPUB/MOBI): "); v != "" {
		nuevo.Formato = v
	}

	_, err = db.Exec(
		`UPDATE libros
		 SET titulo = ?, autor = ?, anio = ?, isbn = ?, categoria = ?, precio = ?, formato = ?
		 WHERE id = ?`,
		nuevo.Titulo, nuevo.Autor, nuevo.Anio, nuevo.ISBN,
		nuevo.Categoria, nuevo.Precio, nuevo.Formato, id,
	)
	if err != nil {
		fmt.Printf("❌ Error al actualizar: %v\n", err)
		return
	}
	fmt.Println("✅ Libro actualizado correctamente.")
}

// eliminarLibro (DELETE): borra un libro por su ID.
func eliminarLibro(db *sql.DB) {
	fmt.Println("\n--- Eliminar libro ---")
	id := leerEntero("ID del libro a eliminar: ")

	res, err := db.Exec("DELETE FROM libros WHERE id = ?", id)
	if err != nil {
		fmt.Printf("❌ Error al eliminar: %v\n", err)
		return
	}
	filas, _ := res.RowsAffected()
	if filas == 0 {
		fmt.Printf("⚠️  No existe ningún libro con ID %d.\n", id)
		return
	}
	fmt.Printf("🗑️  Libro con ID %d eliminado.\n", id)
}

// ---------------------------------------------------------------------------
// UTILIDADES DE BASE DE DATOS
// ---------------------------------------------------------------------------

// crearTabla crea la tabla libros (con todas las columnas) si no existe.
func crearTabla(db *sql.DB) error {
	const sqlStmt = `
	CREATE TABLE IF NOT EXISTS libros (
		id        INTEGER PRIMARY KEY AUTOINCREMENT,
		titulo    TEXT    NOT NULL,
		autor     TEXT    NOT NULL,
		anio      INTEGER NOT NULL,
		isbn      TEXT    DEFAULT '',
		categoria TEXT    DEFAULT '',
		precio    REAL    DEFAULT 0,
		formato   TEXT    DEFAULT ''
	);`
	_, err := db.Exec(sqlStmt)
	return err
}

// migrarTabla agrega las columnas nuevas a bases de datos antiguas
// que fueron creadas antes de ampliar el modelo. No pierde datos.
func migrarTabla(db *sql.DB) error {
	// Columnas que debe tener la tabla, con su definición.
	nuevas := map[string]string{
		"isbn":      "TEXT DEFAULT ''",
		"categoria": "TEXT DEFAULT ''",
		"precio":    "REAL DEFAULT 0",
		"formato":   "TEXT DEFAULT ''",
	}

	// Averiguamos qué columnas ya existen.
	existentes, err := columnasExistentes(db)
	if err != nil {
		return err
	}

	for col, definicion := range nuevas {
		if existentes[col] {
			continue // ya existe, no hacemos nada
		}
		sql := fmt.Sprintf("ALTER TABLE libros ADD COLUMN %s %s", col, definicion)
		if _, err := db.Exec(sql); err != nil {
			return fmt.Errorf("agregando columna %s: %w", col, err)
		}
		fmt.Printf("🔧 Columna '%s' agregada a la tabla.\n", col)
	}
	return nil
}

// columnasExistentes devuelve el conjunto de nombres de columnas de la tabla libros.
func columnasExistentes(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query("PRAGMA table_info(libros)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var (
			cid        int
			nombre     string
			tipo       string
			notNull    int
			porDefecto sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &nombre, &tipo, &notNull, &porDefecto, &pk); err != nil {
			return nil, err
		}
		cols[nombre] = true
	}
	return cols, rows.Err()
}

// obtenerLibro trae un libro completo por su ID.
func obtenerLibro(db *sql.DB, id int64) (Libro, error) {
	var l Libro
	err := db.QueryRow(
		`SELECT id, titulo, autor, anio, isbn, categoria, precio, formato
		 FROM libros WHERE id = ?`, id,
	).Scan(&l.ID, &l.Titulo, &l.Autor, &l.Anio,
		&l.ISBN, &l.Categoria, &l.Precio, &l.Formato)
	return l, err
}

// ---------------------------------------------------------------------------
// UTILIDADES DE ENTRADA POR TECLADO
// ---------------------------------------------------------------------------

// leerTexto muestra un mensaje y devuelve lo que el usuario escribe (sin el salto de línea).
func leerTexto(mensaje string) string {
	fmt.Print(mensaje)
	texto, _ := lector.ReadString('\n')
	return strings.TrimSpace(texto)
}

// leerEntero muestra un mensaje y no regresa hasta obtener un número entero válido.
func leerEntero(mensaje string) int {
	for {
		texto := leerTexto(mensaje)
		numero, err := strconv.Atoi(texto)
		if err != nil {
			fmt.Println("⚠️  Ingresa un número válido.")
			continue
		}
		return numero
	}
}

// leerDecimal muestra un mensaje y no regresa hasta obtener un número decimal válido.
// Acepta coma o punto como separador decimal (ej: 12,50 o 12.50).
func leerDecimal(mensaje string) float64 {
	for {
		texto := leerTexto(mensaje)
		texto = strings.Replace(texto, ",", ".", 1)
		numero, err := strconv.ParseFloat(texto, 64)
		if err != nil {
			fmt.Println("⚠️  Ingresa un precio válido (ej: 12.50).")
			continue
		}
		return numero
	}
}
