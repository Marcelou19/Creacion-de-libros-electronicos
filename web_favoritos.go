package main

// ---------------------------------------------------------------------------
// FAVORITOS
// ---------------------------------------------------------------------------
//
// La estrella del catálogo y la pantalla /favoritos. Se apoyan en la tabla
// `favoritos` que crea migracion2 (main.go), cuya clave primaria es el propio
// libro_id: marcar es un INSERT y desmarcar un DELETE, sin filas repetidas.
//
// No hay usuario_id: la aplicación no tiene inicio de sesión, así que el
// favorito es del catálogo y lo ve cualquiera que abra la página.

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
)

// columnaFavorito se suma al SELECT del catálogo para resolver en la MISMA
// consulta si cada libro está marcado. Con un EXISTS basta: no hace falta
// traer la tabla de favoritos ni hacer una consulta por fila.
const columnaFavorito = `, EXISTS(SELECT 1 FROM favoritos f WHERE f.libro_id = v.id)`

// destinoSeguro decide a dónde volver después de marcar. Solo acepta rutas de
// esta misma aplicación: si llegara una URL externa se ignora, para que el
// formulario no pueda usarse como trampolín hacia otro sitio.
func destinoSeguro(v, porDefecto string) string {
	if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		return v
	}
	return porDefecto
}

// alternarFavorito marca o desmarca según cómo esté ahora, para que el mismo
// botón sirva de ida y de vuelta.
func (s *servidor) alternarFavorito(w http.ResponseWriter, r *http.Request) {
	volver := destinoSeguro(r.FormValue("volver"), "/")

	id, ok := idDeRuta(r)
	if !ok {
		redirigirError(w, r, volver, "ID inválido.")
		return
	}

	var titulo string
	if err := s.db.QueryRow("SELECT titulo FROM libros WHERE id = ?", id).Scan(&titulo); err != nil {
		if err == sql.ErrNoRows {
			redirigirError(w, r, volver, fmt.Sprintf("No existe ningún libro con ID %d.", id))
			return
		}
		redirigirError(w, r, volver, describirError(err))
		return
	}

	var marcado bool
	if err := s.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM favoritos WHERE libro_id = ?)", id).Scan(&marcado); err != nil {
		redirigirError(w, r, volver, describirError(err))
		return
	}

	if marcado {
		if _, err := s.db.Exec("DELETE FROM favoritos WHERE libro_id = ?", id); err != nil {
			redirigirError(w, r, volver, "No se pudo quitar de favoritos: "+describirError(err))
			return
		}
		redirigirOK(w, r, volver, fmt.Sprintf("«%s» salió de Favoritos.", titulo))
		return
	}
	if _, err := s.db.Exec("INSERT INTO favoritos (libro_id) VALUES (?)", id); err != nil {
		redirigirError(w, r, volver, "No se pudo marcar como favorito: "+describirError(err))
		return
	}
	redirigirOK(w, r, volver, fmt.Sprintf("«%s» se agregó a Favoritos.", titulo))
}

// filaFavorito es una fila del catálogo más la fecha en que se marcó.
type filaFavorito struct {
	filaCatalogo
	Fecha string
}

type datosFavoritos struct {
	marco
	Libros []filaFavorito
	Total  int
	Valor  float64
}

func (s *servidor) favoritos(w http.ResponseWriter, r *http.Request) {
	d := datosFavoritos{marco: leerMarco(r, "Favoritos", "favoritos")}

	// JOIN y no LEFT JOIN: aquí solo interesan los marcados. El orden pone
	// arriba lo último que se marcó.
	rows, err := s.db.Query(`
		SELECT ` + columnasCatalogo + `, f.fecha
		FROM v_catalogo v
		JOIN favoritos f ON f.libro_id = v.id
		ORDER BY f.fecha DESC, v.titulo`)
	if err != nil {
		d.Aviso, d.AvisoOK = "Error al consultar: "+err.Error(), false
		dibujar(w, "favoritos", d)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var f filaFavorito
		if err := rows.Scan(&f.ID, &f.Titulo, &f.Autores, &f.Anio, &f.ISBN,
			&f.Categoria, &f.Precio, &f.Formato, &f.Fecha); err != nil {
			d.Aviso, d.AvisoOK = "Error al leer una fila: "+err.Error(), false
			break
		}
		f.Favorito = true // por definición: salen de la tabla favoritos
		d.Libros = append(d.Libros, f)
		d.Valor += f.Precio
	}
	d.Total = len(d.Libros)
	dibujar(w, "favoritos", d)
}
