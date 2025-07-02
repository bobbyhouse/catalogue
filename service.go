package catalogue

// service.go contains the definition and implementation (business logic) of the
// catalogue service. Everything here is agnostic to the transport (HTTP).

import (
	"errors"
	"strings"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/jmoiron/sqlx"
	"github.com/pborman/uuid"
)

// Service is the catalogue service, providing read operations on a saleable
// catalogue of sock products.
type Service interface {
	List(tags []string, order string, pageNum, pageSize int) ([]Sock, error) // GET /catalogue
	Count(tags []string) (int, error)                                        // GET /catalogue/size
	Get(id string) (Sock, error)                                             // GET /catalogue/{id}
	Tags() ([]string, error)                                                 // GET /tags
	Health() []Health                                                        // GET /health
	Create(sock Sock) error                                                  // POST /catalogue
}

// Middleware decorates a Service.
type Middleware func(Service) Service

// Sock describes the thing on offer in the catalogue.
type Sock struct {
	ID          string   `json:"id" db:"id"`
	Name        string   `json:"name" db:"name"`
	Description string   `json:"description" db:"description"`
	ImageURL    []string `json:"imageUrl" db:"-"`
	ImageURL_1  string   `json:"-" db:"image_url_1"`
	ImageURL_2  string   `json:"-" db:"image_url_2"`
	Price       float32  `json:"price" db:"price"`
	Count       int      `json:"count" db:"count"`
	Tags        []string `json:"tag" db:"-"`
	TagString   string   `json:"-" db:"tag_name"`
}

// Health describes the health of a service
type Health struct {
	Service string `json:"service"`
	Status  string `json:"status"`
	Time    string `json:"time"`
}

// ErrNotFound is returned when there is no sock for a given ID.
var ErrNotFound = errors.New("not found")

// ErrDBConnection is returned when connection with the database fails.
var ErrDBConnection = errors.New("database connection error")

var baseQuery = "SELECT sock.sock_id AS id, sock.name, sock.description, sock.price, sock.count, sock.image_url_1, sock.image_url_2, GROUP_CONCAT(tag.name) AS tag_name FROM sock JOIN sock_tag ON sock.sock_id=sock_tag.sock_id JOIN tag ON sock_tag.tag_id=tag.tag_id"

// NewCatalogueService returns an implementation of the Service interface,
// with connection to an SQL database.
func NewCatalogueService(db *sqlx.DB, logger log.Logger) Service {
	return &catalogueService{
		db:     db,
		logger: logger,
	}
}

type catalogueService struct {
	db     *sqlx.DB
	logger log.Logger
}

func (s *catalogueService) List(tags []string, order string, pageNum, pageSize int) ([]Sock, error) {
	var socks []Sock
	query := baseQuery

	var args []interface{}

	for i, t := range tags {
		if i == 0 {
			query += " WHERE tag.name=?"
			args = append(args, t)
		} else {
			query += " OR tag.name=?"
			args = append(args, t)
		}
	}

	query += " GROUP BY id"

	if order != "" {
		query += " ORDER BY ?"
		args = append(args, order)
	}

	query += ";"

	err := s.db.Select(&socks, query, args...)
	if err != nil {
		s.logger.Log("database error", err)
		return []Sock{}, ErrDBConnection
	}
	for i, s := range socks {
		socks[i].ImageURL = []string{s.ImageURL_1, s.ImageURL_2}
		socks[i].Tags = strings.Split(s.TagString, ",")
	}

	// DEMO: Change 0 to 850
	time.Sleep(0 * time.Millisecond)

	socks = cut(socks, pageNum, pageSize)

	return socks, nil
}

func (s *catalogueService) Count(tags []string) (int, error) {
	query := "SELECT COUNT(DISTINCT sock.sock_id) FROM sock JOIN sock_tag ON sock.sock_id=sock_tag.sock_id JOIN tag ON sock_tag.tag_id=tag.tag_id"

	var args []interface{}

	for i, t := range tags {
		if i == 0 {
			query += " WHERE tag.name=?"
			args = append(args, t)
		} else {
			query += " OR tag.name=?"
			args = append(args, t)
		}
	}

	query += ";"

	sel, err := s.db.Prepare(query)

	if err != nil {
		s.logger.Log("database error", err)
		return 0, ErrDBConnection
	}
	defer sel.Close()

	var count int
	err = sel.QueryRow(args...).Scan(&count)

	if err != nil {
		s.logger.Log("database error", err)
		return 0, ErrDBConnection
	}

	return count, nil
}

func (s *catalogueService) Get(id string) (Sock, error) {
	query := baseQuery + " WHERE sock.sock_id =? GROUP BY sock.sock_id;"

	var sock Sock
	err := s.db.Get(&sock, query, id)
	if err != nil {
		s.logger.Log("database error", err)
		return Sock{}, ErrNotFound
	}

	sock.ImageURL = []string{sock.ImageURL_1, sock.ImageURL_2}
	sock.Tags = strings.Split(sock.TagString, ",")

	return sock, nil
}

func (s *catalogueService) Health() []Health {
	var health []Health
	dbstatus := "OK"

	err := s.db.Ping()
	if err != nil {
		dbstatus = "err"
	}

	app := Health{"catalogue", "OK", time.Now().String()}
	db := Health{"catalogue-db", dbstatus, time.Now().String()}

	health = append(health, app)
	health = append(health, db)

	return health
}

func (s *catalogueService) Tags() ([]string, error) {
	var tags []string
	query := "SELECT name FROM tag;"
	rows, err := s.db.Query(query)
	if err != nil {
		s.logger.Log("database error", err)
		return []string{}, ErrDBConnection
	}
	var tag string
	for rows.Next() {
		err = rows.Scan(&tag)
		if err != nil {
			s.logger.Log("database error", err)
			continue
		}
		tags = append(tags, tag)
	}
	return tags, nil
}

func cut(socks []Sock, pageNum, pageSize int) []Sock {
	if pageNum == 0 || pageSize == 0 {
		return []Sock{} // pageNum is 1-indexed
	}
	start := (pageNum * pageSize) - pageSize
	if start > len(socks) {
		return []Sock{}
	}
	end := (pageNum * pageSize)
	if end > len(socks) {
		end = len(socks)
	}
	return socks[start:end]
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func (s *catalogueService) Create(sock Sock) error {
	if sock.ID == "" {
		sock.ID = uuid.NewUUID().String()
	}
	// Insert into sock table
	_, err := s.db.Exec(
		"INSERT INTO sock (sock_id, name, description, price, count, image_url_1, image_url_2) VALUES (?, ?, ?, ?, ?, ?, ?)",
		sock.ID, sock.Name, sock.Description, sock.Price, sock.Count,
		sock.ImageURL[0], sock.ImageURL[1],
	)
	if err != nil {
		s.logger.Log("database error", err)
		return ErrDBConnection
	}
	// Insert tags if they don't exist, and into sock_tag
	for _, tag := range sock.Tags {
		var tagID int
		err = s.db.Get(&tagID, "SELECT tag_id FROM tag WHERE name = ?", tag)
		if err != nil {
			// Tag does not exist, insert it
			res, err2 := s.db.Exec("INSERT INTO tag (name) VALUES (?)", tag)
			if err2 != nil {
				s.logger.Log("database error", err2)
				return ErrDBConnection
			}
			id, err2 := res.LastInsertId()
			if err2 != nil {
				s.logger.Log("database error", err2)
				return ErrDBConnection
			}
			tagID = int(id)
		}
		// Insert into sock_tag
		_, err = s.db.Exec("INSERT INTO sock_tag (sock_id, tag_id) VALUES (?, ?)", sock.ID, tagID)
		if err != nil {
			s.logger.Log("database error", err)
			return ErrDBConnection
		}
	}
	return nil
}
