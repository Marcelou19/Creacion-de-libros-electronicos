package main

// ---------------------------------------------------------------------------
// INTERFAZ WEB
// ---------------------------------------------------------------------------
//
// Este archivo NO duplica la lógica de negocio: reutiliza las mismas funciones
// que usa la consola (obtenerLibro, obtenerOCrear, guardarAutores,
// describirError, textoONulo, patronLike, separarPorComa) y las mismas
// consultas SQL de los reportes. Lo único que agrega es el transporte HTTP y
// las plantillas HTML.
//
// Se arranca con:   ./gestor-libros -web        (http://localhost:8090)
//                   ./gestor-libros -web :9000  (otro puerto)
//
// No hay inicio de sesión a propósito: el servidor escucha solo en localhost.

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Las plantillas y el CSS van DENTRO del binario: `go build` produce un solo
// archivo que se puede copiar a cualquier parte sin arrastrar la carpeta web/.
//
//go:embed web/plantillas/*.html web/estatico/*
var archivosWeb embed.FS

// paginas guarda una plantilla ya compilada por cada pantalla. Cada una es
// base.html + su propio archivo, porque todas definen un bloque "contenido"
// y no pueden convivir en el mismo conjunto.
var paginas = map[string]*template.Template{}

// funcionesPlantilla son los ayudantes que las plantillas pueden llamar.
var funcionesPlantilla = template.FuncMap{
	"dinero": func(v float64) string { return fmt.Sprintf("$%.2f", v) },
	"guion":  textoOGuion,
	"unir":   func(s []string) string { return strings.Join(s, ", ") },
	"mas1":   func(i int) int { return i + 1 },
}

func init() {
	for _, nombre := range []string{"catalogo", "detalle", "formulario", "reportes", "auditoria"} {
		paginas[nombre] = template.Must(
			template.New(nombre).Funcs(funcionesPlantilla).ParseFS(archivosWeb,
				"web/plantillas/base.html",
				"web/plantillas/"+nombre+".html"))
	}
}

// servidor agrupa lo que necesitan todos los manejadores.
type servidor struct{ db *sql.DB }

// marco es lo que toda pantalla comparte: título, sección activa del menú y
// el aviso (verde o rojo) que se muestra arriba del contenido.
type marco struct {
	Titulo  string
	Seccion string
	Aviso   string
	AvisoOK bool
}

func servirWeb(db *sql.DB, direccion string) error {
	s := &servidor{db: db}
	mux := http.NewServeMux()

	estaticos, err := fs.Sub(archivosWeb, "web/estatico")
	if err != nil {
		return err
	}
	mux.Handle("GET /estatico/", http.StripPrefix("/estatico/", http.FileServer(http.FS(estaticos))))

	mux.HandleFunc("GET /{$}", s.catalogo)
	mux.HandleFunc("GET /libro/nuevo", s.formularioNuevo)
	mux.HandleFunc("POST /libro/nuevo", s.crear)
	mux.HandleFunc("GET /libro/{id}", s.detalle)
	mux.HandleFunc("GET /libro/{id}/editar", s.formularioEditar)
	mux.HandleFunc("POST /libro/{id}/editar", s.actualizar)
	mux.HandleFunc("POST /libro/{id}/eliminar", s.eliminar)
	mux.HandleFunc("GET /reportes", s.reportes)
	mux.HandleFunc("GET /auditoria", s.auditoria)

	fmt.Printf("\nGestor de Libros — interfaz web\n")
	fmt.Printf("   Escuchando en http://localhost%s\n", direccion)
	fmt.Printf("   Base de datos: libros.db\n")
	fmt.Printf("   (Ctrl+C para detener)\n\n")
	return http.ListenAndServe(direccion, mux)
}

// ---------------------------------------------------------------------------
// AYUDANTES
// ---------------------------------------------------------------------------

// dibujar ejecuta la plantilla y, si falla, lo deja en el log en vez de
// mandar medio HTML roto al navegador.
func dibujar(w http.ResponseWriter, pagina string, datos any) {
	t, ok := paginas[pagina]
	if !ok {
		http.Error(w, "plantilla desconocida: "+pagina, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base", datos); err != nil {
		log.Printf("error al dibujar %s: %v", pagina, err)
	}
}

// leerMarco arma la cabecera de la página tomando el aviso de la URL.
// Se usa la URL y no una cookie de sesión para no guardar estado en el
// servidor: tras un POST se redirige con ?ok=... o ?error=... (patrón
// Post/Redirect/Get, que evita reenviar el formulario al recargar).
func leerMarco(r *http.Request, titulo, seccion string) marco {
	m := marco{Titulo: titulo, Seccion: seccion}
	if v := r.URL.Query().Get("ok"); v != "" {
		m.Aviso, m.AvisoOK = v, true
	} else if v := r.URL.Query().Get("error"); v != "" {
		m.Aviso, m.AvisoOK = v, false
	}
	return m
}

func redirigirOK(w http.ResponseWriter, r *http.Request, destino, mensaje string) {
	http.Redirect(w, r, destino+"?ok="+url.QueryEscape(mensaje), http.StatusSeeOther)
}

func redirigirError(w http.ResponseWriter, r *http.Request, destino, mensaje string) {
	http.Redirect(w, r, destino+"?error="+url.QueryEscape(mensaje), http.StatusSeeOther)
}

func idDeRuta(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// nombresDe devuelve la lista de autores o categorías ya registrados, para
// alimentar el <datalist> de los formularios (autocompletado del navegador).
func (s *servidor) nombresDe(tabla string) []string {
	rows, err := s.db.Query("SELECT nombre FROM " + tabla + " ORDER BY nombre")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			out = append(out, n)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// CATÁLOGO  (equivale a las opciones 2 y 3 del menú de consola)
// ---------------------------------------------------------------------------

type filaCatalogo struct {
	ID        int64
	Titulo    string
	Autores   string
	Anio      int
	ISBN      string
	Categoria string
	Precio    float64
	Formato   string
}

type datosCatalogo struct {
	marco
	Busqueda string
	Libros   []filaCatalogo
	Total    int
	Valor    float64
}

func (s *servidor) catalogo(w http.ResponseWriter, r *http.Request) {
	busqueda := strings.TrimSpace(r.URL.Query().Get("q"))
	d := datosCatalogo{marco: leerMarco(r, "Catálogo", "catalogo"), Busqueda: busqueda}

	var (
		rows *sql.Rows
		err  error
	)
	if busqueda == "" {
		rows, err = s.db.Query(`SELECT ` + columnasCatalogo + ` FROM v_catalogo v ORDER BY v.id`)
	} else {
		// Misma consulta que buscarGeneral: título O autores, sin tildes.
		patron := patronLike(busqueda)
		rows, err = s.db.Query(`
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
	}
	if err != nil {
		d.Aviso, d.AvisoOK = "Error al consultar: "+err.Error(), false
		dibujar(w, "catalogo", d)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var f filaCatalogo
		if err := rows.Scan(&f.ID, &f.Titulo, &f.Autores, &f.Anio, &f.ISBN,
			&f.Categoria, &f.Precio, &f.Formato); err != nil {
			d.Aviso, d.AvisoOK = "Error al leer una fila: "+err.Error(), false
			break
		}
		d.Libros = append(d.Libros, f)
		d.Valor += f.Precio
	}
	d.Total = len(d.Libros)
	dibujar(w, "catalogo", d)
}

// ---------------------------------------------------------------------------
// DETALLE  (ficha del libro + su historial)
// ---------------------------------------------------------------------------

type datosDetalle struct {
	marco
	Libro      Libro
	Movimiento []filaAuditoria
}

func (s *servidor) detalle(w http.ResponseWriter, r *http.Request) {
	id, ok := idDeRuta(r)
	if !ok {
		redirigirError(w, r, "/", "ID inválido.")
		return
	}
	l, err := obtenerLibro(s.db, id)
	if err == sql.ErrNoRows {
		redirigirError(w, r, "/", fmt.Sprintf("No existe ningún libro con ID %d.", id))
		return
	}
	if err != nil {
		redirigirError(w, r, "/", "Error al buscar: "+err.Error())
		return
	}
	d := datosDetalle{marco: leerMarco(r, l.Titulo, "catalogo"), Libro: l}
	d.Movimiento, _ = s.leerAuditoria(strconv.FormatInt(id, 10), 15)
	dibujar(w, "detalle", d)
}

// ---------------------------------------------------------------------------
// ALTA Y EDICIÓN  (opciones 1 y 4 del menú de consola)
// ---------------------------------------------------------------------------

type datosFormulario struct {
	marco
	Libro      Libro
	AutoresTxt string
	Editando   bool
	Accion     string
	Formatos   []string
	Categorias []string
	Autores    []string
}

func (s *servidor) formularioBase(r *http.Request, titulo string) datosFormulario {
	return datosFormulario{
		marco:      leerMarco(r, titulo, "catalogo"),
		Formatos:   formatosValidos,
		Categorias: s.nombresDe("categorias"),
		Autores:    s.nombresDe("autores"),
	}
}

func (s *servidor) formularioNuevo(w http.ResponseWriter, r *http.Request) {
	d := s.formularioBase(r, "Nuevo libro")
	d.Seccion = "nuevo"
	d.Accion = "/libro/nuevo"
	d.Libro.Anio = 2026
	dibujar(w, "formulario", d)
}

func (s *servidor) formularioEditar(w http.ResponseWriter, r *http.Request) {
	id, ok := idDeRuta(r)
	if !ok {
		redirigirError(w, r, "/", "ID inválido.")
		return
	}
	l, err := obtenerLibro(s.db, id)
	if err == sql.ErrNoRows {
		redirigirError(w, r, "/", fmt.Sprintf("No existe ningún libro con ID %d.", id))
		return
	}
	if err != nil {
		redirigirError(w, r, "/", "Error: "+err.Error())
		return
	}
	d := s.formularioBase(r, "Editar libro")
	d.Libro = l
	d.AutoresTxt = strings.Join(l.Autores, ", ")
	d.Editando = true
	d.Accion = fmt.Sprintf("/libro/%d/editar", id)
	dibujar(w, "formulario", d)
}

// leerFormularioLibro traduce lo que llegó del navegador a un Libro,
// validando lo mismo que valida la consola antes de tocar la base.
func leerFormularioLibro(r *http.Request) (Libro, string) {
	var l Libro
	l.Titulo = strings.TrimSpace(r.FormValue("titulo"))
	if l.Titulo == "" {
		return l, "El título es obligatorio."
	}
	l.Autores = separarPorComa(r.FormValue("autores"))

	anio, err := strconv.Atoi(strings.TrimSpace(r.FormValue("anio")))
	if err != nil {
		return l, "El año debe ser un número."
	}
	if anio < 1450 || anio > 2100 {
		return l, "El año debe estar entre 1450 y 2100."
	}
	l.Anio = anio

	precioTxt := strings.TrimSpace(r.FormValue("precio"))
	if precioTxt == "" {
		precioTxt = "0"
	}
	precio, err := strconv.ParseFloat(strings.Replace(precioTxt, ",", ".", 1), 64)
	if err != nil || precio < 0 {
		return l, "El precio debe ser un número mayor o igual a cero."
	}
	l.Precio = precio

	l.ISBN = strings.TrimSpace(r.FormValue("isbn"))
	l.Categoria = strings.TrimSpace(r.FormValue("categoria"))

	l.Formato = strings.ToUpper(strings.TrimSpace(r.FormValue("formato")))
	if l.Formato != "" {
		valido := false
		for _, f := range formatosValidos {
			if f == l.Formato {
				valido = true
			}
		}
		if !valido {
			return l, "Formato no válido. Usa " + strings.Join(formatosValidos, ", ") + "."
		}
	}
	return l, ""
}

func (s *servidor) crear(w http.ResponseWriter, r *http.Request) {
	l, problema := leerFormularioLibro(r)
	if problema != "" {
		redirigirError(w, r, "/libro/nuevo", problema)
		return
	}

	// Misma transacción que agregarLibro: el libro y sus autores entran juntos.
	tx, err := s.db.Begin()
	if err != nil {
		redirigirError(w, r, "/libro/nuevo", err.Error())
		return
	}
	defer tx.Rollback()

	categoriaID, err := obtenerOCrear(tx, "categorias", l.Categoria)
	if err != nil {
		redirigirError(w, r, "/libro/nuevo", "Categoría: "+describirError(err))
		return
	}
	res, err := tx.Exec(
		`INSERT INTO libros (titulo, anio, isbn, categoria_id, precio, formato)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		l.Titulo, l.Anio, textoONulo(l.ISBN), categoriaID, l.Precio, textoONulo(l.Formato))
	if err != nil {
		redirigirError(w, r, "/libro/nuevo", "No se pudo agregar: "+describirError(err))
		return
	}
	libroID, _ := res.LastInsertId()

	if err := guardarAutores(tx, libroID, l.Autores); err != nil {
		redirigirError(w, r, "/libro/nuevo", "Autores: "+describirError(err))
		return
	}
	if err := tx.Commit(); err != nil {
		redirigirError(w, r, "/libro/nuevo", describirError(err))
		return
	}
	redirigirOK(w, r, fmt.Sprintf("/libro/%d", libroID),
		fmt.Sprintf("Libro agregado con ID %d.", libroID))
}

func (s *servidor) actualizar(w http.ResponseWriter, r *http.Request) {
	id, ok := idDeRuta(r)
	if !ok {
		redirigirError(w, r, "/", "ID inválido.")
		return
	}
	destino := fmt.Sprintf("/libro/%d/editar", id)

	l, problema := leerFormularioLibro(r)
	if problema != "" {
		redirigirError(w, r, destino, problema)
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		redirigirError(w, r, destino, err.Error())
		return
	}
	defer tx.Rollback()

	categoriaID, err := obtenerOCrear(tx, "categorias", l.Categoria)
	if err != nil {
		redirigirError(w, r, destino, "Categoría: "+describirError(err))
		return
	}
	res, err := tx.Exec(
		`UPDATE libros
		 SET titulo = ?, anio = ?, isbn = ?, categoria_id = ?, precio = ?, formato = ?
		 WHERE id = ?`,
		l.Titulo, l.Anio, textoONulo(l.ISBN), categoriaID, l.Precio,
		textoONulo(l.Formato), id)
	if err != nil {
		redirigirError(w, r, destino, "No se pudo actualizar: "+describirError(err))
		return
	}
	if filas, _ := res.RowsAffected(); filas == 0 {
		redirigirError(w, r, "/", fmt.Sprintf("No existe ningún libro con ID %d.", id))
		return
	}

	// El formulario web siempre manda la lista completa de autores, así que
	// se reemplaza entera (se borran los vínculos, no los autores).
	if _, err := tx.Exec(`DELETE FROM libro_autor WHERE libro_id = ?`, id); err != nil {
		redirigirError(w, r, destino, "Al limpiar autores: "+err.Error())
		return
	}
	if err := guardarAutores(tx, id, l.Autores); err != nil {
		redirigirError(w, r, destino, "Autores: "+describirError(err))
		return
	}
	if err := tx.Commit(); err != nil {
		redirigirError(w, r, destino, describirError(err))
		return
	}
	redirigirOK(w, r, fmt.Sprintf("/libro/%d", id), "Libro actualizado correctamente.")
}

func (s *servidor) eliminar(w http.ResponseWriter, r *http.Request) {
	id, ok := idDeRuta(r)
	if !ok {
		redirigirError(w, r, "/", "ID inválido.")
		return
	}
	// libro_autor se limpia solo por el ON DELETE CASCADE; los autores y la
	// categoría se conservan porque pueden pertenecer a otros libros.
	res, err := s.db.Exec("DELETE FROM libros WHERE id = ?", id)
	if err != nil {
		redirigirError(w, r, "/", "No se pudo eliminar: "+describirError(err))
		return
	}
	if filas, _ := res.RowsAffected(); filas == 0 {
		redirigirError(w, r, "/", fmt.Sprintf("No existe ningún libro con ID %d.", id))
		return
	}
	redirigirOK(w, r, "/", fmt.Sprintf("Libro con ID %d eliminado (queda en la auditoría).", id))
}

// ---------------------------------------------------------------------------
// REPORTES  (opción 6 del menú de consola)
// ---------------------------------------------------------------------------

type resumen struct {
	Total, Autores, Categorias int
	Suma, Promedio, Min, Max   float64
	AnioMin, AnioMax           int64
}

type filaCategoria struct {
	Categoria string
	Cantidad  int
	Promedio  float64
	Total     float64
}

type filaAutor struct {
	Autor    string
	Cantidad int
}

type filaFormato struct {
	Formato  string
	Cantidad int
	Promedio float64
}

type filaCaro struct {
	Titulo  string
	Autores string
	Precio  float64
}

type planConsulta struct {
	Titulo string
	SQL    string
	Pasos  []string
	Error  string
	UsaIdx bool
}

type datosReportes struct {
	marco
	Minimo     int
	Resumen    resumen
	Categorias []filaCategoria
	Autores    []filaAutor
	Formatos   []filaFormato
	MasCaros   []filaCaro
	Planes     []planConsulta
}

func (s *servidor) reportes(w http.ResponseWriter, r *http.Request) {
	d := datosReportes{marco: leerMarco(r, "Reportes y estadísticas", "reportes"), Minimo: 1}
	if v, err := strconv.Atoi(r.URL.Query().Get("min")); err == nil && v > 0 {
		d.Minimo = v
	}

	// --- Resumen general: una sola consulta con varias agregaciones.
	var (
		suma, prom, mn, mx sql.NullFloat64
		aMin, aMax         sql.NullInt64
	)
	err := s.db.QueryRow(`
		SELECT COUNT(*), SUM(precio), AVG(precio), MIN(precio), MAX(precio),
		       MIN(anio), MAX(anio),
		       (SELECT COUNT(*) FROM autores),
		       (SELECT COUNT(*) FROM categorias)
		FROM libros`).Scan(&d.Resumen.Total, &suma, &prom, &mn, &mx, &aMin, &aMax,
		&d.Resumen.Autores, &d.Resumen.Categorias)
	if err != nil {
		d.Aviso, d.AvisoOK = "Error en el resumen: "+err.Error(), false
	}
	d.Resumen.Suma, d.Resumen.Promedio = suma.Float64, prom.Float64
	d.Resumen.Min, d.Resumen.Max = mn.Float64, mx.Float64
	d.Resumen.AnioMin, d.Resumen.AnioMax = aMin.Int64, aMax.Int64

	// --- Libros por categoría: GROUP BY sobre un LEFT JOIN.
	if rows, err := s.db.Query(`
		SELECT COALESCE(c.nombre, '(sin categoría)'),
		       COUNT(*), ROUND(AVG(l.precio), 2), ROUND(SUM(l.precio), 2)
		FROM libros l
		LEFT JOIN categorias c ON c.id = l.categoria_id
		GROUP BY l.categoria_id
		ORDER BY 2 DESC, 1`); err == nil {
		for rows.Next() {
			var f filaCategoria
			if rows.Scan(&f.Categoria, &f.Cantidad, &f.Promedio, &f.Total) == nil {
				d.Categorias = append(d.Categorias, f)
			}
		}
		rows.Close()
	}

	// --- Autores más publicados: JOIN + GROUP BY + HAVING + LIMIT.
	if rows, err := s.db.Query(`
		SELECT a.nombre, COUNT(la.libro_id) AS cantidad
		FROM autores a
		JOIN libro_autor la ON la.autor_id = a.id
		GROUP BY a.id
		HAVING COUNT(la.libro_id) >= ?
		ORDER BY cantidad DESC, a.nombre
		LIMIT 10`, d.Minimo); err == nil {
		for rows.Next() {
			var f filaAutor
			if rows.Scan(&f.Autor, &f.Cantidad) == nil {
				d.Autores = append(d.Autores, f)
			}
		}
		rows.Close()
	}

	// --- Libros por formato.
	if rows, err := s.db.Query(`
		SELECT COALESCE(formato, '(sin formato)'), COUNT(*), ROUND(AVG(precio), 2)
		FROM libros GROUP BY formato ORDER BY 2 DESC`); err == nil {
		for rows.Next() {
			var f filaFormato
			if rows.Scan(&f.Formato, &f.Cantidad, &f.Promedio) == nil {
				d.Formatos = append(d.Formatos, f)
			}
		}
		rows.Close()
	}

	// --- Los 5 más caros.
	if rows, err := s.db.Query(`
		SELECT titulo, autores, precio FROM v_catalogo
		ORDER BY precio DESC, titulo LIMIT 5`); err == nil {
		for rows.Next() {
			var f filaCaro
			if rows.Scan(&f.Titulo, &f.Autores, &f.Precio) == nil {
				d.MasCaros = append(d.MasCaros, f)
			}
		}
		rows.Close()
	}

	// --- Plan de ejecución: sirve para comprobar que los índices se usan.
	// Va en un slice (no en un map) para que el orden sea siempre el mismo.
	consultas := []struct{ titulo, sql string }{
		{"Buscar por año (usa idx_libros_anio)", "SELECT * FROM libros WHERE anio = 2007"},
		{"Buscar por precio (no hay índice)", "SELECT * FROM libros WHERE precio > 10"},
		{"Libros de un autor (usa la tabla puente)", "SELECT l.titulo FROM libros l JOIN libro_autor la ON la.libro_id = l.id WHERE la.autor_id = 1"},
	}
	for _, c := range consultas {
		p := planConsulta{Titulo: c.titulo, SQL: c.sql}
		rows, err := s.db.Query("EXPLAIN QUERY PLAN " + c.sql)
		if err != nil {
			p.Error = err.Error()
			d.Planes = append(d.Planes, p)
			continue
		}
		for rows.Next() {
			var a, b, cc int
			var detalle string
			if rows.Scan(&a, &b, &cc, &detalle) != nil {
				break
			}
			p.Pasos = append(p.Pasos, detalle)
			if strings.Contains(detalle, "USING INDEX") || strings.Contains(detalle, "USING COVERING INDEX") {
				p.UsaIdx = true
			}
		}
		rows.Close()
		d.Planes = append(d.Planes, p)
	}

	dibujar(w, "reportes", d)
}

// ---------------------------------------------------------------------------
// AUDITORÍA  (opción 7 del menú de consola)
// ---------------------------------------------------------------------------

type filaAuditoria struct {
	Fecha     string
	LibroID   int64
	Operacion string
	Campo     string
	Antes     string
	Despues   string
}

type datosAuditoria struct {
	marco
	Filtro string
	Filas  []filaAuditoria
}

// leerAuditoria consulta la bitácora que llenan los triggers. La aplicación
// NUNCA escribe en esa tabla: lo hace la base de datos por su cuenta.
func (s *servidor) leerAuditoria(libroID string, limite int) ([]filaAuditoria, error) {
	consulta := `SELECT fecha, libro_id, operacion,
	                    COALESCE(campo, '—'),
	                    COALESCE(valor_anterior, '—'),
	                    COALESCE(valor_nuevo, '—')
	             FROM libros_auditoria`
	args := []any{}
	if libroID != "" {
		consulta += " WHERE libro_id = ?"
		args = append(args, libroID)
	}
	consulta += fmt.Sprintf(" ORDER BY id DESC LIMIT %d", limite)

	rows, err := s.db.Query(consulta, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []filaAuditoria
	for rows.Next() {
		var f filaAuditoria
		if err := rows.Scan(&f.Fecha, &f.LibroID, &f.Operacion, &f.Campo,
			&f.Antes, &f.Despues); err != nil {
			return out, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *servidor) auditoria(w http.ResponseWriter, r *http.Request) {
	d := datosAuditoria{marco: leerMarco(r, "Historial de cambios", "auditoria")}
	d.Filtro = strings.TrimSpace(r.URL.Query().Get("libro"))
	if d.Filtro != "" {
		if _, err := strconv.Atoi(d.Filtro); err != nil {
			d.Aviso, d.AvisoOK = "El filtro debe ser un ID numérico.", false
			d.Filtro = ""
		}
	}
	filas, err := s.leerAuditoria(d.Filtro, 200)
	if err != nil {
		d.Aviso, d.AvisoOK = "Error: "+err.Error(), false
	}
	d.Filas = filas
	dibujar(w, "auditoria", d)
}
