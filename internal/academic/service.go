package academic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/optional"
)

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

type OrgUnit struct {
	ID       string  `json:"id"`
	ParentID *string `json:"parent_id"`
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
}
type CreateOrgUnit struct {
	ParentID optional.Value[string] `json:"parent_id"`
	Code     string                 `json:"code"`
	Name     string                 `json:"name"`
	Kind     string                 `json:"kind"`
}
type PatchOrgUnit struct {
	ParentID optional.Value[string] `json:"parent_id"`
	Code     *string                `json:"code"`
	Name     *string                `json:"name"`
	Kind     *string                `json:"kind"`
}
type Term struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}
type CreateTerm struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}
type PatchTerm struct {
	Code      *string `json:"code"`
	Name      *string `json:"name"`
	StartDate *string `json:"start_date"`
	EndDate   *string `json:"end_date"`
}
type Course struct {
	ID          string `json:"id"`
	OrgUnitID   string `json:"org_unit_id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
type CreateCourse struct {
	OrgUnitID   string `json:"org_unit_id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
type PatchCourse struct {
	OrgUnitID   *string `json:"org_unit_id"`
	Code        *string `json:"code"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
}
type ClassGroup struct {
	ID             string `json:"id"`
	OrgUnitID      string `json:"org_unit_id"`
	Code           string `json:"code"`
	Name           string `json:"name"`
	EnrollmentYear *int   `json:"enrollment_year"`
	ExpectedSize   *int   `json:"expected_size"`
}
type CreateClassGroup struct {
	OrgUnitID      string              `json:"org_unit_id"`
	Code           string              `json:"code"`
	Name           string              `json:"name"`
	EnrollmentYear int                 `json:"enrollment_year"`
	ExpectedSize   optional.Value[int] `json:"expected_size"`
}
type PatchClassGroup struct {
	OrgUnitID      *string             `json:"org_unit_id"`
	Code           *string             `json:"code"`
	Name           *string             `json:"name"`
	EnrollmentYear *int                `json:"enrollment_year"`
	ExpectedSize   optional.Value[int] `json:"expected_size"`
}
type Classroom struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Capacity *int   `json:"capacity"`
}
type CreateClassroom struct {
	Code     string              `json:"code"`
	Name     string              `json:"name"`
	Capacity optional.Value[int] `json:"capacity"`
}
type PatchClassroom struct {
	Code     *string             `json:"code"`
	Name     *string             `json:"name"`
	Capacity optional.Value[int] `json:"capacity"`
}
type Offering struct {
	ID           string `json:"id"`
	OrgUnitID    string `json:"org_unit_id"`
	TermID       string `json:"term_id"`
	CourseID     string `json:"course_id"`
	TeacherID    string `json:"teacher_id"`
	ClassGroupID string `json:"class_group_id"`
	Code         string `json:"code"`
	Status       string `json:"status"`
}
type CreateOffering struct {
	OrgUnitID    string `json:"org_unit_id"`
	TermID       string `json:"term_id"`
	CourseID     string `json:"course_id"`
	TeacherID    string `json:"teacher_id"`
	ClassGroupID string `json:"class_group_id"`
	Code         string `json:"code"`
	Status       string `json:"status"`
}
type PatchOffering struct {
	OrgUnitID    *string `json:"org_unit_id"`
	TermID       *string `json:"term_id"`
	CourseID     *string `json:"course_id"`
	TeacherID    *string `json:"teacher_id"`
	ClassGroupID *string `json:"class_group_id"`
	Code         *string `json:"code"`
	Status       *string `json:"status"`
}

func requireSystem(p identity.Principal) error {
	if !p.Has("sys_admin") {
		return apperror.New(http.StatusForbidden, "FORBIDDEN", "system administrator permission is required")
	}
	return nil
}
func clean(value string) string { return strings.TrimSpace(value) }
func nextID[T any](items []T, limit int, id func(T) string) ([]T, string) {
	if len(items) <= limit {
		return items, ""
	}
	next := id(items[limit-1])
	return items[:limit], next
}
func notFound(name string) error {
	return apperror.New(http.StatusNotFound, "NOT_FOUND", name+" not found")
}
func invalid(message string) error {
	return apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", message)
}

func (s *Service) canReadCollege(ctx context.Context, p identity.Principal, orgID string, teacherResource, resourceID string) (bool, error) {
	if p.Has("sys_admin") {
		return true, nil
	}
	var college string
	if err := s.pool.QueryRow(ctx, `SELECT teaching.college_of($1)`, orgID).Scan(&college); err != nil {
		return false, nil
	}
	if p.Scoped("academic_admin", college) || p.Scoped("supervisor", college) {
		return true, nil
	}
	if !p.Has("teacher") {
		return false, nil
	}
	var allowed bool
	switch teacherResource {
	case "course":
		err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.course_offerings WHERE teacher_id=$1 AND course_id=$2)`, p.UserID, resourceID).Scan(&allowed)
		return allowed, err
	case "class":
		err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.course_offerings WHERE teacher_id=$1 AND class_group_id=$2)`, p.UserID, resourceID).Scan(&allowed)
		return allowed, err
	case "offering":
		err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.course_offerings WHERE teacher_id=$1 AND id=$2)`, p.UserID, resourceID).Scan(&allowed)
		return allowed, err
	}
	return false, nil
}
func (s *Service) canManageOrg(ctx context.Context, p identity.Principal, orgID string) (bool, error) {
	if p.Has("sys_admin") {
		return true, nil
	}
	var college *string
	if err := s.pool.QueryRow(ctx, `SELECT teaching.college_of($1)`, orgID).Scan(&college); err != nil {
		return false, err
	}
	return college != nil && p.Scoped("academic_admin", *college), nil
}

func (s *Service) ListOrgUnits(ctx context.Context, p identity.Principal, after string, limit int) ([]OrgUnit, string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,parent_id::text,code,name,kind FROM teaching.org_units WHERE ($1='' OR id>$1::uuid) AND ($2 OR id=ANY($3::uuid[]) OR teaching.college_of(id)=ANY($3::uuid[])) ORDER BY id LIMIT $4`, after, p.Has("sys_admin"), p.CollegeScopes(), limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []OrgUnit{}
	for rows.Next() {
		var v OrgUnit
		if err := rows.Scan(&v.ID, &v.ParentID, &v.Code, &v.Name, &v.Kind); err != nil {
			return nil, "", err
		}
		items = append(items, v)
	}
	items, next := nextID(items, limit, func(v OrgUnit) string { return v.ID })
	return items, next, rows.Err()
}
func (s *Service) GetOrgUnit(ctx context.Context, p identity.Principal, id string) (OrgUnit, error) {
	var v OrgUnit
	err := s.pool.QueryRow(ctx, `SELECT id::text,parent_id::text,code,name,kind FROM teaching.org_units WHERE id=$1`, id).Scan(&v.ID, &v.ParentID, &v.Code, &v.Name, &v.Kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrgUnit{}, notFound("organization")
	}
	if err != nil {
		return OrgUnit{}, err
	}
	ok, err := s.canManageOrg(ctx, p, id)
	if err != nil {
		return OrgUnit{}, err
	}
	if !ok {
		var college *string
		if err := s.pool.QueryRow(ctx, `SELECT teaching.college_of($1)`, id).Scan(&college); err != nil {
			return OrgUnit{}, err
		}
		if college == nil || !p.Scoped("supervisor", *college) {
			return OrgUnit{}, notFound("organization")
		}
	}
	return v, nil
}
func (s *Service) CreateOrgUnit(ctx context.Context, p identity.Principal, in CreateOrgUnit) (OrgUnit, error) {
	if err := requireSystem(p); err != nil {
		return OrgUnit{}, err
	}
	if !in.ParentID.Set {
		return OrgUnit{}, invalid("parent_id is required")
	}
	if err := s.validateOrg(ctx, "", in.ParentID.Value, in.Kind); err != nil {
		return OrgUnit{}, err
	}
	if clean(in.Code) == "" || clean(in.Name) == "" {
		return OrgUnit{}, invalid("organization code and name are required")
	}
	var v OrgUnit
	err := s.pool.QueryRow(ctx, `INSERT INTO teaching.org_units(parent_id,code,name,kind) VALUES($1,$2,$3,$4) RETURNING id::text,parent_id::text,code,name,kind`, in.ParentID.Value, clean(in.Code), clean(in.Name), in.Kind).Scan(&v.ID, &v.ParentID, &v.Code, &v.Name, &v.Kind)
	return v, dbError(err)
}
func (s *Service) PatchOrgUnit(ctx context.Context, p identity.Principal, id string, in PatchOrgUnit) (OrgUnit, error) {
	if err := requireSystem(p); err != nil {
		return OrgUnit{}, err
	}
	v, err := s.GetOrgUnit(ctx, p, id)
	if err != nil {
		return OrgUnit{}, err
	}
	parent := v.ParentID
	if in.ParentID.Set {
		parent = in.ParentID.Value
	}
	kind := v.Kind
	if in.Kind != nil {
		kind = *in.Kind
	}
	if in.ParentID.Set || in.Kind != nil {
		referenced, err := s.orgReferenced(ctx, id)
		if err != nil {
			return OrgUnit{}, err
		}
		if referenced {
			return OrgUnit{}, apperror.New(http.StatusConflict, "INVALID_STATE", "referenced organization ownership cannot change")
		}
	}
	if err := s.validateOrg(ctx, id, parent, kind); err != nil {
		return OrgUnit{}, err
	}
	code := v.Code
	if in.Code != nil {
		code = clean(*in.Code)
	}
	name := v.Name
	if in.Name != nil {
		name = clean(*in.Name)
	}
	if code == "" || name == "" {
		return OrgUnit{}, invalid("organization code and name are required")
	}
	err = s.pool.QueryRow(ctx, `UPDATE teaching.org_units SET parent_id=$2,code=$3,name=$4,kind=$5 WHERE id=$1 RETURNING id::text,parent_id::text,code,name,kind`, id, parent, code, name, kind).Scan(&v.ID, &v.ParentID, &v.Code, &v.Name, &v.Kind)
	return v, dbError(err)
}
func (s *Service) validateOrg(ctx context.Context, id string, parent *string, kind string) error {
	if kind != "school" && kind != "college" && kind != "department" {
		return invalid("invalid organization kind")
	}
	if kind == "school" {
		if parent != nil {
			return invalid("school cannot have a parent")
		}
		return nil
	}
	if parent == nil {
		return invalid("college and department require a parent")
	}
	var parentKind string
	err := s.pool.QueryRow(ctx, `SELECT kind FROM teaching.org_units WHERE id=$1`, *parent).Scan(&parentKind)
	if errors.Is(err, pgx.ErrNoRows) {
		return invalid("parent organization does not exist")
	}
	if err != nil {
		return dbError(err)
	}
	if kind == "college" && parentKind != "school" || kind == "department" && parentKind != "college" {
		return invalid("organization hierarchy is invalid")
	}
	if id != "" {
		var cycle bool
		err = s.pool.QueryRow(ctx, `WITH RECURSIVE descendants AS(SELECT id FROM teaching.org_units WHERE id=$1 UNION ALL SELECT o.id FROM teaching.org_units o JOIN descendants d ON o.parent_id=d.id) SELECT $2::uuid IN(SELECT id FROM descendants)`, id, *parent).Scan(&cycle)
		if err != nil {
			return dbError(err)
		}
		if cycle {
			return invalid("organization hierarchy cannot contain a cycle")
		}
	}
	return nil
}
func (s *Service) orgReferenced(ctx context.Context, id string) (bool, error) {
	var yes bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.user_accounts WHERE org_unit_id=$1 UNION ALL SELECT 1 FROM teaching.courses WHERE org_unit_id=$1 UNION ALL SELECT 1 FROM teaching.class_groups WHERE org_unit_id=$1 UNION ALL SELECT 1 FROM teaching.course_offerings WHERE org_unit_id=$1)`, id).Scan(&yes)
	return yes, err
}

func (s *Service) ListTerms(ctx context.Context, _ identity.Principal, after string, limit int) ([]Term, string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,code,name,start_date::text,end_date::text FROM teaching.academic_terms WHERE ($1='' OR id>$1::uuid) ORDER BY id LIMIT $2`, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []Term{}
	for rows.Next() {
		var v Term
		if err := rows.Scan(&v.ID, &v.Code, &v.Name, &v.StartDate, &v.EndDate); err != nil {
			return nil, "", err
		}
		items = append(items, v)
	}
	items, next := nextID(items, limit, func(v Term) string { return v.ID })
	return items, next, rows.Err()
}
func (s *Service) GetTerm(ctx context.Context, _ identity.Principal, id string) (Term, error) {
	var v Term
	err := s.pool.QueryRow(ctx, `SELECT id::text,code,name,start_date::text,end_date::text FROM teaching.academic_terms WHERE id=$1`, id).Scan(&v.ID, &v.Code, &v.Name, &v.StartDate, &v.EndDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return Term{}, notFound("term")
	}
	return v, err
}
func (s *Service) CreateTerm(ctx context.Context, p identity.Principal, in CreateTerm) (Term, error) {
	if err := requireSystem(p); err != nil {
		return Term{}, err
	}
	if clean(in.Code) == "" || clean(in.Name) == "" {
		return Term{}, invalid("term code and name are required")
	}
	start, end, err := dates(in.StartDate, in.EndDate)
	if err != nil {
		return Term{}, err
	}
	var v Term
	err = s.pool.QueryRow(ctx, `INSERT INTO teaching.academic_terms(code,name,start_date,end_date) VALUES($1,$2,$3,$4) RETURNING id::text,code,name,start_date::text,end_date::text`, clean(in.Code), clean(in.Name), start, end).Scan(&v.ID, &v.Code, &v.Name, &v.StartDate, &v.EndDate)
	return v, dbError(err)
}
func (s *Service) PatchTerm(ctx context.Context, p identity.Principal, id string, in PatchTerm) (Term, error) {
	if err := requireSystem(p); err != nil {
		return Term{}, err
	}
	v, err := s.GetTerm(ctx, p, id)
	if err != nil {
		return Term{}, err
	}
	code := v.Code
	if in.Code != nil {
		code = clean(*in.Code)
	}
	name := v.Name
	if in.Name != nil {
		name = clean(*in.Name)
	}
	if code == "" || name == "" {
		return Term{}, invalid("term code and name are required")
	}
	startRaw, endRaw := v.StartDate, v.EndDate
	if in.StartDate != nil {
		startRaw = *in.StartDate
	}
	if in.EndDate != nil {
		endRaw = *in.EndDate
	}
	start, end, err := dates(startRaw, endRaw)
	if err != nil {
		return Term{}, err
	}
	err = s.pool.QueryRow(ctx, `UPDATE teaching.academic_terms SET code=$2,name=$3,start_date=$4,end_date=$5 WHERE id=$1 RETURNING id::text,code,name,start_date::text,end_date::text`, id, code, name, start, end).Scan(&v.ID, &v.Code, &v.Name, &v.StartDate, &v.EndDate)
	return v, dbError(err)
}
func dates(a, b string) (time.Time, time.Time, error) {
	start, e1 := time.Parse("2006-01-02", a)
	end, e2 := time.Parse("2006-01-02", b)
	if e1 != nil || e2 != nil || !end.After(start) {
		return time.Time{}, time.Time{}, invalid("term dates are invalid")
	}
	return start, end, nil
}

func (s *Service) ListCourses(ctx context.Context, p identity.Principal, after string, limit int) ([]Course, string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,org_unit_id::text,code,name,description FROM teaching.courses c WHERE ($1='' OR id>$1::uuid) AND ($3 OR teaching.college_of(c.org_unit_id)=ANY($4::uuid[]) OR EXISTS(SELECT 1 FROM teaching.course_offerings o WHERE o.course_id=c.id AND o.teacher_id=$5)) ORDER BY id LIMIT $2`, after, limit+1, p.Has("sys_admin"), p.CollegeScopes(), p.UserID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []Course{}
	for rows.Next() {
		var v Course
		if err := rows.Scan(&v.ID, &v.OrgUnitID, &v.Code, &v.Name, &v.Description); err != nil {
			return nil, "", err
		}
		items = append(items, v)
	}
	items, next := nextID(items, limit, func(v Course) string { return v.ID })
	return items, next, rows.Err()
}
func (s *Service) GetCourse(ctx context.Context, p identity.Principal, id string) (Course, error) {
	var v Course
	err := s.pool.QueryRow(ctx, `SELECT id::text,org_unit_id::text,code,name,description FROM teaching.courses WHERE id=$1`, id).Scan(&v.ID, &v.OrgUnitID, &v.Code, &v.Name, &v.Description)
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, notFound("course")
	}
	if err != nil {
		return Course{}, err
	}
	ok, err := s.canReadCollege(ctx, p, v.OrgUnitID, "course", id)
	if err != nil {
		return Course{}, err
	}
	if !ok {
		return Course{}, notFound("course")
	}
	return v, nil
}
func (s *Service) CreateCourse(ctx context.Context, p identity.Principal, in CreateCourse) (Course, error) {
	ok, err := s.canManageOrg(ctx, p, in.OrgUnitID)
	if err != nil {
		return Course{}, err
	}
	if !ok {
		return Course{}, apperror.New(http.StatusForbidden, "FORBIDDEN", "organization is outside your scope")
	}
	if clean(in.Code) == "" || clean(in.Name) == "" {
		return Course{}, invalid("course code and name are required")
	}
	var v Course
	err = s.pool.QueryRow(ctx, `INSERT INTO teaching.courses(org_unit_id,code,name,description) VALUES($1,$2,$3,$4) RETURNING id::text,org_unit_id::text,code,name,description`, in.OrgUnitID, clean(in.Code), clean(in.Name), in.Description).Scan(&v.ID, &v.OrgUnitID, &v.Code, &v.Name, &v.Description)
	return v, dbError(err)
}
func (s *Service) PatchCourse(ctx context.Context, p identity.Principal, id string, in PatchCourse) (Course, error) {
	v, err := s.GetCourse(ctx, p, id)
	if err != nil {
		return Course{}, err
	}
	org := v.OrgUnitID
	if in.OrgUnitID != nil {
		org = *in.OrgUnitID
		if org != v.OrgUnitID {
			var used bool
			if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.course_offerings WHERE course_id=$1)`, id).Scan(&used); err != nil {
				return Course{}, err
			}
			if used {
				return Course{}, apperror.New(http.StatusConflict, "INVALID_STATE", "referenced course ownership cannot change")
			}
		}
	}
	ok, err := s.canManageOrg(ctx, p, org)
	if err != nil || !ok {
		if err != nil {
			return Course{}, err
		}
		return Course{}, apperror.New(http.StatusForbidden, "FORBIDDEN", "organization is outside your scope")
	}
	code, name, description := v.Code, v.Name, v.Description
	if in.Code != nil {
		code = clean(*in.Code)
	}
	if in.Name != nil {
		name = clean(*in.Name)
	}
	if in.Description != nil {
		description = *in.Description
	}
	if code == "" || name == "" {
		return Course{}, invalid("course code and name are required")
	}
	err = s.pool.QueryRow(ctx, `UPDATE teaching.courses SET org_unit_id=$2,code=$3,name=$4,description=$5 WHERE id=$1 RETURNING id::text,org_unit_id::text,code,name,description`, id, org, code, name, description).Scan(&v.ID, &v.OrgUnitID, &v.Code, &v.Name, &v.Description)
	return v, dbError(err)
}

func (s *Service) ListClassGroups(ctx context.Context, p identity.Principal, after string, limit int) ([]ClassGroup, string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,org_unit_id::text,code,name,enrollment_year,expected_size FROM teaching.class_groups g WHERE ($1='' OR id>$1::uuid) AND ($3 OR teaching.college_of(g.org_unit_id)=ANY($4::uuid[]) OR EXISTS(SELECT 1 FROM teaching.course_offerings o WHERE o.class_group_id=g.id AND o.teacher_id=$5)) ORDER BY id LIMIT $2`, after, limit+1, p.Has("sys_admin"), p.CollegeScopes(), p.UserID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []ClassGroup{}
	for rows.Next() {
		var v ClassGroup
		if err := rows.Scan(&v.ID, &v.OrgUnitID, &v.Code, &v.Name, &v.EnrollmentYear, &v.ExpectedSize); err != nil {
			return nil, "", err
		}
		items = append(items, v)
	}
	items, next := nextID(items, limit, func(v ClassGroup) string { return v.ID })
	return items, next, rows.Err()
}
func (s *Service) GetClassGroup(ctx context.Context, p identity.Principal, id string) (ClassGroup, error) {
	var v ClassGroup
	err := s.pool.QueryRow(ctx, `SELECT id::text,org_unit_id::text,code,name,enrollment_year,expected_size FROM teaching.class_groups WHERE id=$1`, id).Scan(&v.ID, &v.OrgUnitID, &v.Code, &v.Name, &v.EnrollmentYear, &v.ExpectedSize)
	if errors.Is(err, pgx.ErrNoRows) {
		return ClassGroup{}, notFound("class group")
	}
	if err != nil {
		return ClassGroup{}, err
	}
	ok, err := s.canReadCollege(ctx, p, v.OrgUnitID, "class", id)
	if err != nil {
		return ClassGroup{}, err
	}
	if !ok {
		return ClassGroup{}, notFound("class group")
	}
	return v, nil
}
func (s *Service) CreateClassGroup(ctx context.Context, p identity.Principal, in CreateClassGroup) (ClassGroup, error) {
	ok, err := s.canManageOrg(ctx, p, in.OrgUnitID)
	if err != nil || !ok {
		if err != nil {
			return ClassGroup{}, err
		}
		return ClassGroup{}, apperror.New(http.StatusForbidden, "FORBIDDEN", "organization is outside your scope")
	}
	if in.EnrollmentYear < 1900 || in.EnrollmentYear > 2200 {
		return ClassGroup{}, invalid("enrollment year is invalid")
	}
	if !in.ExpectedSize.Set {
		return ClassGroup{}, invalid("expected_size is required")
	}
	if clean(in.Code) == "" || clean(in.Name) == "" {
		return ClassGroup{}, invalid("class code and name are required")
	}
	var v ClassGroup
	err = s.pool.QueryRow(ctx, `INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year,expected_size) VALUES($1,$2,$3,$4,$5) RETURNING id::text,org_unit_id::text,code,name,enrollment_year,expected_size`, in.OrgUnitID, clean(in.Code), clean(in.Name), in.EnrollmentYear, in.ExpectedSize.Value).Scan(&v.ID, &v.OrgUnitID, &v.Code, &v.Name, &v.EnrollmentYear, &v.ExpectedSize)
	return v, dbError(err)
}
func (s *Service) PatchClassGroup(ctx context.Context, p identity.Principal, id string, in PatchClassGroup) (ClassGroup, error) {
	v, err := s.GetClassGroup(ctx, p, id)
	if err != nil {
		return ClassGroup{}, err
	}
	org := v.OrgUnitID
	if in.OrgUnitID != nil {
		org = *in.OrgUnitID
		if org != v.OrgUnitID {
			var used bool
			if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.course_offerings WHERE class_group_id=$1)`, id).Scan(&used); err != nil {
				return ClassGroup{}, err
			}
			if used {
				return ClassGroup{}, apperror.New(http.StatusConflict, "INVALID_STATE", "referenced class ownership cannot change")
			}
		}
	}
	ok, err := s.canManageOrg(ctx, p, org)
	if err != nil || !ok {
		if err != nil {
			return ClassGroup{}, err
		}
		return ClassGroup{}, apperror.New(http.StatusForbidden, "FORBIDDEN", "organization is outside your scope")
	}
	code, name := v.Code, v.Name
	year, size := v.EnrollmentYear, v.ExpectedSize
	if in.Code != nil {
		code = clean(*in.Code)
	}
	if in.Name != nil {
		name = clean(*in.Name)
	}
	if in.EnrollmentYear != nil {
		year = in.EnrollmentYear
	}
	if in.ExpectedSize.Set {
		size = in.ExpectedSize.Value
	}
	if code == "" || name == "" {
		return ClassGroup{}, invalid("class code and name are required")
	}
	if year == nil || *year < 1900 || *year > 2200 {
		return ClassGroup{}, invalid("enrollment year is invalid")
	}
	err = s.pool.QueryRow(ctx, `UPDATE teaching.class_groups SET org_unit_id=$2,code=$3,name=$4,enrollment_year=$5,expected_size=$6 WHERE id=$1 RETURNING id::text,org_unit_id::text,code,name,enrollment_year,expected_size`, id, org, code, name, year, size).Scan(&v.ID, &v.OrgUnitID, &v.Code, &v.Name, &v.EnrollmentYear, &v.ExpectedSize)
	return v, dbError(err)
}

func (s *Service) ListClassrooms(ctx context.Context, _ identity.Principal, after string, limit int) ([]Classroom, string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,code,name,capacity FROM teaching.classrooms WHERE ($1='' OR id>$1::uuid) ORDER BY id LIMIT $2`, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []Classroom{}
	for rows.Next() {
		var v Classroom
		if err := rows.Scan(&v.ID, &v.Code, &v.Name, &v.Capacity); err != nil {
			return nil, "", err
		}
		items = append(items, v)
	}
	items, next := nextID(items, limit, func(v Classroom) string { return v.ID })
	return items, next, rows.Err()
}
func (s *Service) GetClassroom(ctx context.Context, _ identity.Principal, id string) (Classroom, error) {
	var v Classroom
	err := s.pool.QueryRow(ctx, `SELECT id::text,code,name,capacity FROM teaching.classrooms WHERE id=$1`, id).Scan(&v.ID, &v.Code, &v.Name, &v.Capacity)
	if errors.Is(err, pgx.ErrNoRows) {
		return Classroom{}, notFound("classroom")
	}
	return v, err
}
func (s *Service) CreateClassroom(ctx context.Context, p identity.Principal, in CreateClassroom) (Classroom, error) {
	if err := requireSystem(p); err != nil {
		return Classroom{}, err
	}
	if !in.Capacity.Set {
		return Classroom{}, invalid("capacity is required")
	}
	if in.Capacity.Value != nil && *in.Capacity.Value <= 0 {
		return Classroom{}, invalid("capacity must be positive")
	}
	if clean(in.Code) == "" || clean(in.Name) == "" {
		return Classroom{}, invalid("classroom code and name are required")
	}
	var v Classroom
	err := s.pool.QueryRow(ctx, `INSERT INTO teaching.classrooms(code,name,capacity) VALUES($1,$2,$3) RETURNING id::text,code,name,capacity`, clean(in.Code), clean(in.Name), in.Capacity.Value).Scan(&v.ID, &v.Code, &v.Name, &v.Capacity)
	return v, dbError(err)
}
func (s *Service) PatchClassroom(ctx context.Context, p identity.Principal, id string, in PatchClassroom) (Classroom, error) {
	if err := requireSystem(p); err != nil {
		return Classroom{}, err
	}
	v, err := s.GetClassroom(ctx, p, id)
	if err != nil {
		return Classroom{}, err
	}
	code, name, size := v.Code, v.Name, v.Capacity
	if in.Code != nil {
		code = clean(*in.Code)
	}
	if in.Name != nil {
		name = clean(*in.Name)
	}
	if in.Capacity.Set {
		size = in.Capacity.Value
	}
	if code == "" || name == "" {
		return Classroom{}, invalid("classroom code and name are required")
	}
	if size != nil && *size <= 0 {
		return Classroom{}, invalid("capacity must be positive")
	}
	err = s.pool.QueryRow(ctx, `UPDATE teaching.classrooms SET code=$2,name=$3,capacity=$4 WHERE id=$1 RETURNING id::text,code,name,capacity`, id, code, name, size).Scan(&v.ID, &v.Code, &v.Name, &v.Capacity)
	return v, dbError(err)
}

func (s *Service) ListOfferings(ctx context.Context, p identity.Principal, after string, limit int) ([]Offering, string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,org_unit_id::text,term_id::text,course_id::text,teacher_id::text,class_group_id::text,code,status FROM teaching.course_offerings o WHERE ($1='' OR id>$1::uuid) AND ($3 OR teaching.college_of(o.org_unit_id)=ANY($4::uuid[]) OR o.teacher_id=$5) ORDER BY id LIMIT $2`, after, limit+1, p.Has("sys_admin"), p.CollegeScopes(), p.UserID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []Offering{}
	for rows.Next() {
		var v Offering
		if err := rows.Scan(&v.ID, &v.OrgUnitID, &v.TermID, &v.CourseID, &v.TeacherID, &v.ClassGroupID, &v.Code, &v.Status); err != nil {
			return nil, "", err
		}
		items = append(items, v)
	}
	items, next := nextID(items, limit, func(v Offering) string { return v.ID })
	return items, next, rows.Err()
}
func (s *Service) GetOffering(ctx context.Context, p identity.Principal, id string) (Offering, error) {
	var v Offering
	err := s.pool.QueryRow(ctx, `SELECT id::text,org_unit_id::text,term_id::text,course_id::text,teacher_id::text,class_group_id::text,code,status FROM teaching.course_offerings WHERE id=$1`, id).Scan(&v.ID, &v.OrgUnitID, &v.TermID, &v.CourseID, &v.TeacherID, &v.ClassGroupID, &v.Code, &v.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Offering{}, notFound("offering")
	}
	if err != nil {
		return Offering{}, err
	}
	ok, err := s.canReadCollege(ctx, p, v.OrgUnitID, "offering", id)
	if err != nil {
		return Offering{}, err
	}
	if !ok {
		return Offering{}, notFound("offering")
	}
	return v, nil
}
func (s *Service) CreateOffering(ctx context.Context, p identity.Principal, in CreateOffering) (Offering, error) {
	if err := s.authorizeOffering(ctx, p, in.OrgUnitID, in.TeacherID); err != nil {
		return Offering{}, err
	}
	if in.Status != "active" && in.Status != "archived" {
		return Offering{}, invalid("invalid offering status")
	}
	if clean(in.Code) == "" {
		return Offering{}, invalid("offering code is required")
	}
	var v Offering
	err := s.pool.QueryRow(ctx, `INSERT INTO teaching.course_offerings(org_unit_id,term_id,course_id,teacher_id,class_group_id,code,status) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text,org_unit_id::text,term_id::text,course_id::text,teacher_id::text,class_group_id::text,code,status`, in.OrgUnitID, in.TermID, in.CourseID, in.TeacherID, in.ClassGroupID, clean(in.Code), in.Status).Scan(&v.ID, &v.OrgUnitID, &v.TermID, &v.CourseID, &v.TeacherID, &v.ClassGroupID, &v.Code, &v.Status)
	return v, dbError(err)
}
func (s *Service) PatchOffering(ctx context.Context, p identity.Principal, id string, in PatchOffering) (Offering, error) {
	v, err := s.GetOffering(ctx, p, id)
	if err != nil {
		return Offering{}, err
	}
	org, term, course, teacher, group, code, status := v.OrgUnitID, v.TermID, v.CourseID, v.TeacherID, v.ClassGroupID, v.Code, v.Status
	if in.OrgUnitID != nil {
		org = *in.OrgUnitID
	}
	if in.TermID != nil {
		term = *in.TermID
	}
	if in.CourseID != nil {
		course = *in.CourseID
	}
	if in.TeacherID != nil {
		teacher = *in.TeacherID
	}
	if in.ClassGroupID != nil {
		group = *in.ClassGroupID
	}
	if in.Code != nil {
		code = clean(*in.Code)
	}
	if in.Status != nil {
		status = *in.Status
	}
	if err := s.authorizeOffering(ctx, p, org, teacher); err != nil {
		return Offering{}, err
	}
	if status != "active" && status != "archived" {
		return Offering{}, invalid("invalid offering status")
	}
	if code == "" {
		return Offering{}, invalid("offering code is required")
	}
	err = s.pool.QueryRow(ctx, `UPDATE teaching.course_offerings SET org_unit_id=$2,term_id=$3,course_id=$4,teacher_id=$5,class_group_id=$6,code=$7,status=$8 WHERE id=$1 RETURNING id::text,org_unit_id::text,term_id::text,course_id::text,teacher_id::text,class_group_id::text,code,status`, id, org, term, course, teacher, group, code, status).Scan(&v.ID, &v.OrgUnitID, &v.TermID, &v.CourseID, &v.TeacherID, &v.ClassGroupID, &v.Code, &v.Status)
	return v, dbError(err)
}
func (s *Service) authorizeOffering(ctx context.Context, p identity.Principal, org, teacher string) error {
	ok, err := s.canManageOrg(ctx, p, org)
	if err != nil {
		return err
	}
	if !ok {
		return apperror.New(http.StatusForbidden, "FORBIDDEN", "organization is outside your scope")
	}
	if p.Has("sys_admin") {
		return nil
	}
	var same bool
	err = s.pool.QueryRow(ctx, `SELECT teaching.college_of(u.org_unit_id)=teaching.college_of($1) FROM teaching.user_accounts u WHERE u.id=$2`, org, teacher).Scan(&same)
	if errors.Is(err, pgx.ErrNoRows) || !same {
		return apperror.New(http.StatusForbidden, "FORBIDDEN", "academic administrators cannot assign a cross-college teacher")
	}
	return err
}

func dbError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return apperror.WithDetails(http.StatusBadRequest, "INVALID_ARGUMENT", "a unique field already exists", map[string]any{"constraint": pgErr.ConstraintName})
		case "23503", "23514", "22P02":
			return invalid("related resource or field is invalid")
		}
	}
	return fmt.Errorf("academic storage: %w", err)
}
