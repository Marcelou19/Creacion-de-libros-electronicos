package main

// ---------------------------------------------------------------------------
// PANTALLAS DE CATÁLOGO: AUTORES, CATEGORÍAS, FORMATOS Y DATOS
// ---------------------------------------------------------------------------
//
// Estas cuatro pantallas son las únicas del menú de imprimirMenuNavegacion()
// que el esquema actual ya soporta:
//
//   Autores / Categorías  -> tienen tabla propia, así que admiten ABM completo.
//   Formatos              -> NO es una tabla: es el CHECK de libros.formato más
//                            formatosValidos. Por eso es solo de consulta.
//   Importar / Exportar   -> CSV y JSON sobre lo que ya existe.
//
// El resto del menú (Editoriales, Colecciones, Usuarios, Préstamos, …) sigue
// en gris en la barra lateral porque no hay tablas donde guardarlo.

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// TAXONOMÍAS (autores y categorías)
// ---------------------------------------------------------------------------
//
// Las dos tablas se editan exactamente igual —id + nombre único— así que
// comparten manejadores y plantilla. Lo único que cambia es el texto y qué
// le pasa a los libros cuando se borra una fila, y eso viaja en esta ficha.

type taxonomia struct {
	Tabla    string // nombre real de la tabla
	Ruta     string // "/autores"
	Seccion  string // para marcar el menú lateral
	Titulo   string // "Autores"
	Singular string // "autor"
	Genero   string // "el" / "la", para redactar los avisos
	Icono    string // símbolo SVG de base.html
	// Efecto explica en la confirmación de borrado qué se lleva por delante.
	Efecto string
	// consulta lista id, nombre y cuántos libros usa cada fila. Lleva un %s
	// donde se inyecta el WHERE del buscador (o cadena vacía).
	consulta string
}

var taxAutores = taxonomia{
	Tabla:    "autores",
	Ruta:     "/autores",
	Seccion:  "autores",
	Titulo:   "Autores",
	Singular: "autor",
	Genero:   "El",
	Icono:    "#i-pluma",
	Efecto: "Los libros NO se borran: solo pierden a ese autor, porque el " +
		"ON DELETE CASCADE de libro_autor limpia el vínculo, no el libro.",
	consulta: `
		SELECT a.id, a.nombre, COUNT(la.libro_id)
		FROM autores a
		LEFT JOIN libro_autor la ON la.autor_id = a.id
		%s
		GROUP BY a.id, a.nombre
		ORDER BY a.nombre`,
}

var taxCategorias = taxonomia{
	Tabla:    "categorias",
	Ruta:     "/categorias",
	Seccion:  "categorias",
	Titulo:   "Categorías",
	Singular: "categoría",
	Genero:   "La",
	Icono:    "#i-etiqueta",
	Efecto: "Los libros NO se borran: quedan como «(sin categoría)», porque " +
		"libros.categoria_id está declarado ON DELETE SET NULL.",
	consulta: `
		SELECT c.id, c.nombre, COUNT(l.id)
		FROM categorias c
		LEFT JOIN libros l ON l.categoria_id = c.id
		%s
		GROUP BY c.id, c.nombre
		ORDER BY c.nombre`,
}

type filaTaxonomia struct {
	ID     int64
	Nombre string
	Libros int
}

type datosTaxonomia struct {
	marco
	Tax      taxonomia
	Busqueda string
	Filas    []filaTaxonomia
	Total    int
	SinUsar  int
}

// listar dibuja la tabla con el conteo de libros de cada fila.
func (s *servidor) listarTaxonomia(t taxonomia) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		busqueda := strings.TrimSpace(r.URL.Query().Get("q"))
		d := datosTaxonomia{
			marco:    leerMarco(r, t.Titulo, t.Seccion),
			Tax:      t,
			Busqueda: busqueda,
		}

		// El buscador ignora tildes y mayúsculas usando la misma función SQL
		// propia que el catálogo (ver registrarFuncionesSQL).
		filtro, args := "", []any{}
		if busqueda != "" {
			filtro = `WHERE sin_acentos(` + t.Tabla[:1] + `.nombre) LIKE sin_acentos(?) ESCAPE '\'`
			args = append(args, patronLike(busqueda))
		}

		rows, err := s.db.Query(fmt.Sprintf(t.consulta, filtro), args...)
		if err != nil {
			d.Aviso, d.AvisoOK = "Error al consultar: "+err.Error(), false
			dibujar(w, "taxonomia", d)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var f filaTaxonomia
			if err := rows.Scan(&f.ID, &f.Nombre, &f.Libros); err != nil {
				d.Aviso, d.AvisoOK = "Error al leer una fila: "+err.Error(), false
				break
			}
			if f.Libros == 0 {
				d.SinUsar++
			}
			d.Filas = append(d.Filas, f)
		}
		d.Total = len(d.Filas)
		dibujar(w, "taxonomia", d)
	}
}

func (s *servidor) crearTaxonomia(t taxonomia) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nombre := strings.TrimSpace(r.FormValue("nombre"))
		if nombre == "" {
			redirigirError(w, r, t.Ruta, "El nombre no puede estar vacío.")
			return
		}
		// UNIQUE COLLATE NOCASE ya impide el duplicado; se consulta antes solo
		// para dar un mensaje claro en vez del error crudo de SQLite.
		var existe string
		err := s.db.QueryRow("SELECT nombre FROM "+t.Tabla+" WHERE nombre = ?", nombre).Scan(&existe)
		if err == nil {
			redirigirError(w, r, t.Ruta, fmt.Sprintf("Ya existe: «%s».", existe))
			return
		}
		if err != sql.ErrNoRows {
			redirigirError(w, r, t.Ruta, describirError(err))
			return
		}
		if _, err := s.db.Exec("INSERT INTO "+t.Tabla+" (nombre) VALUES (?)", nombre); err != nil {
			redirigirError(w, r, t.Ruta, "No se pudo agregar: "+describirError(err))
			return
		}
		redirigirOK(w, r, t.Ruta, fmt.Sprintf("%s %s «%s» se agregó.", t.Genero, t.Singular, nombre))
	}
}

func (s *servidor) renombrarTaxonomia(t taxonomia) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := idDeRuta(r)
		if !ok {
			redirigirError(w, r, t.Ruta, "ID inválido.")
			return
		}
		nombre := strings.TrimSpace(r.FormValue("nombre"))
		if nombre == "" {
			redirigirError(w, r, t.Ruta, "El nombre no puede estar vacío.")
			return
		}
		res, err := s.db.Exec("UPDATE "+t.Tabla+" SET nombre = ? WHERE id = ?", nombre, id)
		if err != nil {
			redirigirError(w, r, t.Ruta, "No se pudo renombrar: "+describirError(err))
			return
		}
		if filas, _ := res.RowsAffected(); filas == 0 {
			redirigirError(w, r, t.Ruta, fmt.Sprintf("No existe ningún registro con ID %d.", id))
			return
		}
		// Al renombrar cambia en TODOS los libros a la vez: no hay texto
		// repetido que actualizar, que es justo la ventaja de tener la tabla.
		redirigirOK(w, r, t.Ruta, fmt.Sprintf("Renombrado a «%s».", nombre))
	}
}

func (s *servidor) eliminarTaxonomia(t taxonomia) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := idDeRuta(r)
		if !ok {
			redirigirError(w, r, t.Ruta, "ID inválido.")
			return
		}
		var nombre string
		if err := s.db.QueryRow("SELECT nombre FROM "+t.Tabla+" WHERE id = ?", id).Scan(&nombre); err != nil {
			redirigirError(w, r, t.Ruta, fmt.Sprintf("No existe ningún registro con ID %d.", id))
			return
		}
		if _, err := s.db.Exec("DELETE FROM "+t.Tabla+" WHERE id = ?", id); err != nil {
			redirigirError(w, r, t.Ruta, "No se pudo eliminar: "+describirError(err))
			return
		}
		redirigirOK(w, r, t.Ruta, fmt.Sprintf("«%s» se eliminó. %s", nombre, t.Efecto))
	}
}

// ---------------------------------------------------------------------------
// FORMATOS  (solo consulta)
// ---------------------------------------------------------------------------

// filaFormatoDetalle NO reutiliza el filaFormato de los reportes: aquel tiene
// solo tres campos y no incluye la suma. Un nombre propio evita que la
// pantalla de reportes acabe con un Total siempre en cero.
type filaFormatoDetalle struct {
	Formato  string
	Cantidad int
	Total    float64
	Promedio float64
}

type datosFormatos struct {
	marco
	Filas []filaFormatoDetalle
	// SinFormato son los libros con formato NULL; ConFormato, el resto.
	// Los dos suman TotalLibros.
	SinFormato  int
	ConFormato  int
	TotalLibros int
}

func (s *servidor) formatos(w http.ResponseWriter, r *http.Request) {
	d := datosFormatos{marco: leerMarco(r, "Formatos", "formatos")}

	// Se arranca con los formatos que permite el CHECK, en cero, para que los
	// que nadie usó todavía también aparezcan en la tabla.
	indice := map[string]int{}
	for i, f := range formatosValidos {
		d.Filas = append(d.Filas, filaFormatoDetalle{Formato: f})
		indice[f] = i
	}

	rows, err := s.db.Query(`
		SELECT COALESCE(formato, ''), COUNT(*), COALESCE(SUM(precio), 0), COALESCE(AVG(precio), 0)
		FROM libros
		GROUP BY formato`)
	if err != nil {
		d.Aviso, d.AvisoOK = "Error al consultar: "+err.Error(), false
		dibujar(w, "formatos", d)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var f filaFormatoDetalle
		if err := rows.Scan(&f.Formato, &f.Cantidad, &f.Total, &f.Promedio); err != nil {
			d.Aviso, d.AvisoOK = "Error al leer una fila: "+err.Error(), false
			break
		}
		d.TotalLibros += f.Cantidad
		if f.Formato == "" { // formato NULL: el libro no declaró ninguno
			d.SinFormato = f.Cantidad
			continue
		}
		if i, ok := indice[f.Formato]; ok {
			d.Filas[i] = f
		}
	}
	d.ConFormato = d.TotalLibros - d.SinFormato
	dibujar(w, "formatos", d)
}

// ---------------------------------------------------------------------------
// IMPORTAR / EXPORTAR
// ---------------------------------------------------------------------------

// columnasCSV es la cabecera del archivo y también el orden que espera la
// importación. Exportar e importar usan la misma lista para que el viaje de
// ida y vuelta no pierda nada.
var columnasCSV = []string{"titulo", "autores", "anio", "isbn", "categoria", "precio", "formato"}

type datosDatos struct {
	marco
	Libros, Autores, Categorias int
	Columnas                    []string
}

func (s *servidor) datos(w http.ResponseWriter, r *http.Request) {
	d := datosDatos{marco: leerMarco(r, "Importar / Exportar", "datos"), Columnas: columnasCSV}
	s.db.QueryRow("SELECT COUNT(*) FROM libros").Scan(&d.Libros)
	s.db.QueryRow("SELECT COUNT(*) FROM autores").Scan(&d.Autores)
	s.db.QueryRow("SELECT COUNT(*) FROM categorias").Scan(&d.Categorias)
	dibujar(w, "datos", d)
}

// libroExportable es un libro tal como sale al archivo: sin ID y con los
// vacíos como cadena vacía (no como "—"), para poder reimportarlo.
type libroExportable struct {
	Titulo    string  `json:"titulo"`
	Autores   string  `json:"autores"`
	Anio      int     `json:"anio"`
	ISBN      string  `json:"isbn"`
	Categoria string  `json:"categoria"`
	Precio    float64 `json:"precio"`
	Formato   string  `json:"formato"`
}

// leerExportables saca el catálogo completo desde las tablas base. No usa
// v_catalogo a propósito: esa vista reemplaza los NULL por "—" y por
// "(sin categoría)", que son adornos para la pantalla, no datos.
func (s *servidor) leerExportables() ([]libroExportable, error) {
	rows, err := s.db.Query(`
		SELECT l.titulo,
		       COALESCE(group_concat(a.nombre, ', '), '') AS autores,
		       l.anio,
		       COALESCE(l.isbn, ''),
		       COALESCE(c.nombre, ''),
		       l.precio,
		       COALESCE(l.formato, '')
		FROM libros l
		LEFT JOIN categorias  c  ON c.id = l.categoria_id
		LEFT JOIN libro_autor la ON la.libro_id = l.id
		LEFT JOIN autores     a  ON a.id = la.autor_id
		GROUP BY l.id
		ORDER BY l.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []libroExportable
	for rows.Next() {
		var e libroExportable
		if err := rows.Scan(&e.Titulo, &e.Autores, &e.Anio, &e.ISBN,
			&e.Categoria, &e.Precio, &e.Formato); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *servidor) exportarCSV(w http.ResponseWriter, r *http.Request) {
	libros, err := s.leerExportables()
	if err != nil {
		redirigirError(w, r, "/datos", "No se pudo exportar: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="catalogo.csv"`)

	// El BOM hace que Excel abra el archivo como UTF-8 y no parta las tildes.
	w.Write([]byte("\xEF\xBB\xBF"))

	c := csv.NewWriter(w)
	defer c.Flush()
	c.Write(columnasCSV)
	for _, l := range libros {
		c.Write([]string{
			l.Titulo, l.Autores, strconv.Itoa(l.Anio), l.ISBN, l.Categoria,
			strconv.FormatFloat(l.Precio, 'f', 2, 64), l.Formato,
		})
	}
}

func (s *servidor) exportarJSON(w http.ResponseWriter, r *http.Request) {
	libros, err := s.leerExportables()
	if err != nil {
		redirigirError(w, r, "/datos", "No se pudo exportar: "+err.Error())
		return
	}
	if libros == nil {
		libros = []libroExportable{} // "[]" y no "null" si no hay nada
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="catalogo.json"`)
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	e.Encode(libros)
}

func (s *servidor) importarCSV(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MB en memoria
		redirigirError(w, r, "/datos", "No se pudo leer el envío: "+err.Error())
		return
	}
	archivo, cabecera, err := r.FormFile("archivo")
	if err != nil {
		redirigirError(w, r, "/datos", "Elige un archivo .csv antes de importar.")
		return
	}
	defer archivo.Close()

	lector := csv.NewReader(quitarBOM(archivo))
	lector.FieldsPerRecord = -1 // se valida a mano para poder decir qué línea falla
	lector.TrimLeadingSpace = true

	filas, err := lector.ReadAll()
	if err != nil {
		redirigirError(w, r, "/datos", "El CSV está mal formado: "+err.Error())
		return
	}
	if len(filas) < 2 {
		redirigirError(w, r, "/datos", "El archivo no tiene filas de datos (solo cabecera o está vacío).")
		return
	}
	if problema := revisarCabecera(filas[0]); problema != "" {
		redirigirError(w, r, "/datos", problema)
		return
	}

	// Todo o nada: una sola transacción. Si la fila 40 tiene un ISBN repetido,
	// no queremos quedarnos con 39 libros a medio importar.
	tx, err := s.db.Begin()
	if err != nil {
		redirigirError(w, r, "/datos", err.Error())
		return
	}
	defer tx.Rollback()

	insertados := 0
	for i, fila := range filas[1:] {
		linea := i + 2 // +1 por la cabecera, +1 porque los humanos cuentan desde 1
		if len(fila) != len(columnasCSV) {
			redirigirError(w, r, "/datos", fmt.Sprintf(
				"Línea %d: tiene %d columnas y se esperaban %d.", linea, len(fila), len(columnasCSV)))
			return
		}
		l, problema := libroDesdeCSV(fila)
		if problema != "" {
			redirigirError(w, r, "/datos", fmt.Sprintf("Línea %d: %s", linea, problema))
			return
		}
		if _, err := insertarLibro(tx, l); err != nil {
			redirigirError(w, r, "/datos", fmt.Sprintf(
				"Línea %d («%s»): %s", linea, l.Titulo, describirError(err)))
			return
		}
		insertados++
	}
	if err := tx.Commit(); err != nil {
		redirigirError(w, r, "/datos", describirError(err))
		return
	}
	redirigirOK(w, r, "/", fmt.Sprintf(
		"Se importaron %d libros desde %s.", insertados, cabecera.Filename))
}

// quitarBOM descarta la marca de UTF-8 que Excel pone al principio; sin esto
// la primera columna se llamaría "<BOM>titulo" y la cabecera no coincidiría.
func quitarBOM(r io.Reader) io.Reader {
	bom := make([]byte, 3)
	n, _ := io.ReadFull(r, bom)
	if n == 3 && bom[0] == 0xEF && bom[1] == 0xBB && bom[2] == 0xBF {
		return r
	}
	return io.MultiReader(strings.NewReader(string(bom[:n])), r)
}

func revisarCabecera(fila []string) string {
	if len(fila) != len(columnasCSV) {
		return fmt.Sprintf("La cabecera debe tener %d columnas: %s.",
			len(columnasCSV), strings.Join(columnasCSV, ", "))
	}
	for i, c := range fila {
		if !strings.EqualFold(strings.TrimSpace(c), columnasCSV[i]) {
			return fmt.Sprintf("La columna %d debería llamarse «%s» y dice «%s».",
				i+1, columnasCSV[i], strings.TrimSpace(c))
		}
	}
	return ""
}

// libroDesdeCSV traduce una fila del archivo a un Libro y lo valida con las
// mismas reglas que el formulario web y que la consola.
func libroDesdeCSV(fila []string) (Libro, string) {
	var l Libro
	l.Titulo = strings.TrimSpace(fila[0])
	l.Autores = separarPorComa(fila[1])

	anio, err := strconv.Atoi(strings.TrimSpace(fila[2]))
	if err != nil {
		return l, "el año debe ser un número."
	}
	l.Anio = anio

	l.ISBN = strings.TrimSpace(fila[3])
	l.Categoria = strings.TrimSpace(fila[4])

	precioTxt := strings.TrimSpace(fila[5])
	if precioTxt == "" {
		precioTxt = "0"
	}
	precio, err := strconv.ParseFloat(strings.Replace(precioTxt, ",", ".", 1), 64)
	if err != nil {
		return l, "el precio debe ser un número."
	}
	l.Precio = precio
	l.Formato = strings.ToUpper(strings.TrimSpace(fila[6]))

	if problema := validarLibro(l); problema != "" {
		return l, strings.ToLower(problema[:1]) + problema[1:]
	}
	return l, ""
}
