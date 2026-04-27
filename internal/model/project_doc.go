package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Project doc types. The four canonical types (concept, architecture,
// workflow, roadmap) live under a partial unique index that limits each
// project to one of each. Custom docs are unlimited - useful for ADRs,
// retrospectives, ad-hoc references that don't fit the four buckets.
const (
	ProjectDocConcept      = "concept"
	ProjectDocArchitecture = "architecture"
	ProjectDocWorkflow     = "workflow"
	ProjectDocRoadmap      = "roadmap"
	ProjectDocCustom       = "custom"
)

// validDocTypes is consulted by the handler before hitting the DB so
// invalid input gets a clean 400 instead of a CHECK constraint
// violation.
var validDocTypes = map[string]struct{}{
	ProjectDocConcept:      {},
	ProjectDocArchitecture: {},
	ProjectDocWorkflow:     {},
	ProjectDocRoadmap:      {},
	ProjectDocCustom:       {},
}

func IsValidProjectDocType(t string) bool {
	_, ok := validDocTypes[t]
	return ok
}

// ProjectDoc is one piece of project documentation. ContentMD is the
// raw markdown the user submitted; ContentHTML is the sanitised HTML
// the handler produced via render.Markdown + render.Sanitizer before
// calling Upsert. The store does not render - mirror of how PostStore
// takes pre-rendered content from PostHandler.
type ProjectDoc struct {
	ID           int64      `json:"id"`
	ProjectID    int64      `json:"project_id"`
	DocType      string     `json:"doc_type"`
	Title        string     `json:"title"`
	ContentMD    string     `json:"content_md"`
	ContentHTML  string     `json:"content_html"`
	SortOrder    int        `json:"sort_order"`
	LastEditedBy *int64     `json:"last_edited_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type ProjectDocStore struct {
	DB *pgxpool.Pool
}

var ErrProjectDocNotFound = errors.New("project doc not found")
var ErrProjectDocInvalidType = errors.New("project doc type invalid")

const projectDocCols = `
	id, project_id, doc_type, title, content_md, content_html,
	sort_order, last_edited_by, created_at, updated_at`

// ProjectDocUpsertParams is what the handler passes after rendering
// content_md to content_html. EditedBy is the user.id of whoever
// triggered the upsert.
type ProjectDocUpsertParams struct {
	ProjectID   int64
	DocType     string
	Title       string
	ContentMD   string
	ContentHTML string
	SortOrder   int
	EditedBy    int64
}

// Upsert creates or updates a doc. For the four canonical doc types
// this matches the partial unique index (project_id, doc_type) WHERE
// doc_type != 'custom' and turns into an UPDATE on conflict. Custom
// docs always insert a new row, which is why they take a separate
// branch.
func (s *ProjectDocStore) Upsert(ctx context.Context, p ProjectDocUpsertParams) (*ProjectDoc, error) {
	if !IsValidProjectDocType(p.DocType) {
		return nil, ErrProjectDocInvalidType
	}

	d := &ProjectDoc{}
	if p.DocType == ProjectDocCustom {
		err := s.DB.QueryRow(ctx, `
			INSERT INTO project_docs
				(project_id, doc_type, title, content_md, content_html,
				 sort_order, last_edited_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING `+projectDocCols,
			p.ProjectID, p.DocType, p.Title, p.ContentMD, p.ContentHTML,
			p.SortOrder, p.EditedBy,
		).Scan(
			&d.ID, &d.ProjectID, &d.DocType, &d.Title, &d.ContentMD,
			&d.ContentHTML, &d.SortOrder, &d.LastEditedBy,
			&d.CreatedAt, &d.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("upsert custom project doc: %w", err)
		}
		return d, nil
	}

	err := s.DB.QueryRow(ctx, `
		INSERT INTO project_docs
			(project_id, doc_type, title, content_md, content_html,
			 sort_order, last_edited_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (project_id, doc_type) WHERE doc_type <> 'custom'
		DO UPDATE SET
			title = EXCLUDED.title,
			content_md = EXCLUDED.content_md,
			content_html = EXCLUDED.content_html,
			sort_order = EXCLUDED.sort_order,
			last_edited_by = EXCLUDED.last_edited_by,
			updated_at = NOW()
		RETURNING `+projectDocCols,
		p.ProjectID, p.DocType, p.Title, p.ContentMD, p.ContentHTML,
		p.SortOrder, p.EditedBy,
	).Scan(
		&d.ID, &d.ProjectID, &d.DocType, &d.Title, &d.ContentMD,
		&d.ContentHTML, &d.SortOrder, &d.LastEditedBy,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert project doc: %w", err)
	}
	return d, nil
}

func (s *ProjectDocStore) ListByProject(ctx context.Context, projectID int64) ([]ProjectDoc, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT `+projectDocCols+`
		 FROM project_docs
		 WHERE project_id = $1
		 ORDER BY sort_order, created_at`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list project docs: %w", err)
	}
	defer rows.Close()

	var out []ProjectDoc
	for rows.Next() {
		var d ProjectDoc
		if err := rows.Scan(
			&d.ID, &d.ProjectID, &d.DocType, &d.Title, &d.ContentMD,
			&d.ContentHTML, &d.SortOrder, &d.LastEditedBy,
			&d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan project doc: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project docs: %w", err)
	}
	return out, nil
}

// GetByType returns one of the four canonical docs. Returns
// ErrProjectDocNotFound for the missing case so the handler can
// distinguish 404 from 500. Custom docs are addressed by id, not by
// type, so this method is non-meaningful for them.
func (s *ProjectDocStore) GetByType(ctx context.Context, projectID int64, docType string) (*ProjectDoc, error) {
	if !IsValidProjectDocType(docType) || docType == ProjectDocCustom {
		return nil, ErrProjectDocInvalidType
	}
	d := &ProjectDoc{}
	err := s.DB.QueryRow(ctx,
		`SELECT `+projectDocCols+`
		 FROM project_docs
		 WHERE project_id = $1 AND doc_type = $2`,
		projectID, docType,
	).Scan(
		&d.ID, &d.ProjectID, &d.DocType, &d.Title, &d.ContentMD,
		&d.ContentHTML, &d.SortOrder, &d.LastEditedBy,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, ErrProjectDocNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get project doc by type: %w", err)
	}
	return d, nil
}

func (s *ProjectDocStore) GetByID(ctx context.Context, id int64) (*ProjectDoc, error) {
	d := &ProjectDoc{}
	err := s.DB.QueryRow(ctx,
		`SELECT `+projectDocCols+`
		 FROM project_docs WHERE id = $1`, id,
	).Scan(
		&d.ID, &d.ProjectID, &d.DocType, &d.Title, &d.ContentMD,
		&d.ContentHTML, &d.SortOrder, &d.LastEditedBy,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, ErrProjectDocNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get project doc by id: %w", err)
	}
	return d, nil
}

// Delete removes a doc row. Used in 16a only for custom docs - the
// four canonical docs stay forever once created (UI clears their
// content if needed) so we don't need a per-type delete branch.
func (s *ProjectDocStore) Delete(ctx context.Context, id int64) error {
	res, err := s.DB.Exec(ctx, `DELETE FROM project_docs WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete project doc: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrProjectDocNotFound
	}
	return nil
}
