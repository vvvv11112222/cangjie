package academic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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

type Schedule struct {
	ID           string    `json:"id"`
	OfferingID   string    `json:"offering_id"`
	ClassroomID  string    `json:"classroom_id"`
	StartsAt     time.Time `json:"starts_at"`
	EndsAt       time.Time `json:"ends_at"`
	Status       string    `json:"status"`
	TeacherID    string    `json:"teacher_id"`
	ClassGroupID string    `json:"class_group_id"`
}

type CreateSchedule struct {
	OfferingID  string `json:"offering_id"`
	ClassroomID string `json:"classroom_id"`
	StartsAt    string `json:"starts_at"`
	EndsAt      string `json:"ends_at"`
	Status      string `json:"status"`
}

type PatchSchedule struct {
	OfferingID  *string `json:"offering_id"`
	ClassroomID *string `json:"classroom_id"`
	StartsAt    *string `json:"starts_at"`
	EndsAt      *string `json:"ends_at"`
	Status      *string `json:"status"`
}

type ImportScheduleRow struct {
	OfferingID  string `json:"offering_id"`
	ClassroomID string `json:"classroom_id"`
	StartsAt    string `json:"starts_at"`
	EndsAt      string `json:"ends_at"`
}

type ImportSchedules struct {
	Rows []ImportScheduleRow `json:"rows"`
}

type ImportResult struct {
	Items []Schedule `json:"items"`
}

type ScheduleQuery struct {
	After        string
	Limit        int
	TeacherID    string
	ClassroomID  string
	ClassGroupID string
	At           *time.Time
	From         *time.Time
	To           *time.Time
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

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func validatePatchOfferingUUIDs(in PatchOffering) error {
	fields := []struct {
		name  string
		value *string
	}{
		{"org_unit_id", in.OrgUnitID},
		{"term_id", in.TermID},
		{"course_id", in.CourseID},
		{"teacher_id", in.TeacherID},
		{"class_group_id", in.ClassGroupID},
	}
	for _, field := range fields {
		if field.value != nil && !validUUID(*field.value) {
			return invalid(field.name + " must be a UUID")
		}
	}
	return nil
}

type rowGetter interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func canManageOrg(ctx context.Context, q rowGetter, p identity.Principal, orgID string) (bool, error) {
	if p.Has("sys_admin") {
		return true, nil
	}
	var college *string
	if err := q.QueryRow(ctx, `SELECT teaching.college_of($1)`, orgID).Scan(&college); err != nil {
		return false, err
	}
	return college != nil && p.Scoped("academic_admin", *college), nil
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
	return canManageOrg(ctx, s.pool, p, orgID)
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
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OrgUnit{}, fmt.Errorf("begin organization update: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `LOCK TABLE teaching.org_units IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return OrgUnit{}, fmt.Errorf("lock organization hierarchy: %w", err)
	}
	if in.ParentID.Set || in.Kind != nil {
		if _, err := tx.Exec(ctx, `LOCK TABLE teaching.user_accounts, teaching.role_bindings, teaching.courses, teaching.class_groups, teaching.course_offerings IN SHARE MODE`); err != nil {
			return OrgUnit{}, fmt.Errorf("lock organization references: %w", err)
		}
	}
	var v OrgUnit
	err = tx.QueryRow(ctx, `SELECT id::text,parent_id::text,code,name,kind FROM teaching.org_units WHERE id=$1 FOR UPDATE`, id).Scan(&v.ID, &v.ParentID, &v.Code, &v.Name, &v.Kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrgUnit{}, notFound("organization")
	}
	if err != nil {
		return OrgUnit{}, dbError(err)
	}
	parent := v.ParentID
	if in.ParentID.Set {
		parent = in.ParentID.Value
	}
	kind := v.Kind
	if in.Kind != nil {
		kind = *in.Kind
	}
	ownershipChanged := !sameStringPointer(parent, v.ParentID) || kind != v.Kind
	if ownershipChanged {
		referenced, err := orgSubtreeReferenced(ctx, tx, id)
		if err != nil {
			return OrgUnit{}, err
		}
		if referenced {
			return OrgUnit{}, apperror.New(http.StatusConflict, "INVALID_STATE", "referenced organization ownership cannot change")
		}
	}
	if err := validateOrg(ctx, tx, id, parent, kind); err != nil {
		return OrgUnit{}, err
	}
	if ownershipChanged {
		var invalidChild bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM teaching.org_units child WHERE child.parent_id=$1 AND NOT (
				($2='school' AND child.kind='college') OR ($2='college' AND child.kind='department')
			)
		)`, id, kind).Scan(&invalidChild)
		if err != nil {
			return OrgUnit{}, dbError(err)
		}
		if invalidChild {
			return OrgUnit{}, apperror.New(http.StatusConflict, "INVALID_STATE", "organization descendants would have an invalid hierarchy")
		}
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
	err = tx.QueryRow(ctx, `UPDATE teaching.org_units SET parent_id=$2,code=$3,name=$4,kind=$5 WHERE id=$1 RETURNING id::text,parent_id::text,code,name,kind`, id, parent, code, name, kind).Scan(&v.ID, &v.ParentID, &v.Code, &v.Name, &v.Kind)
	if err != nil {
		return OrgUnit{}, dbError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OrgUnit{}, dbError(err)
	}
	return v, nil
}
func sameStringPointer(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
func (s *Service) validateOrg(ctx context.Context, id string, parent *string, kind string) error {
	return validateOrg(ctx, s.pool, id, parent, kind)
}
func validateOrg(ctx context.Context, q rowGetter, id string, parent *string, kind string) error {
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
	err := q.QueryRow(ctx, `SELECT kind FROM teaching.org_units WHERE id=$1`, *parent).Scan(&parentKind)
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
		err = q.QueryRow(ctx, `WITH RECURSIVE descendants AS(SELECT id FROM teaching.org_units WHERE id=$1 UNION ALL SELECT o.id FROM teaching.org_units o JOIN descendants d ON o.parent_id=d.id) SELECT $2::uuid IN(SELECT id FROM descendants)`, id, *parent).Scan(&cycle)
		if err != nil {
			return dbError(err)
		}
		if cycle {
			return invalid("organization hierarchy cannot contain a cycle")
		}
	}
	return nil
}
func orgSubtreeReferenced(ctx context.Context, q rowGetter, id string) (bool, error) {
	var yes bool
	err := q.QueryRow(ctx, `WITH RECURSIVE subtree AS (
		SELECT id FROM teaching.org_units WHERE id=$1
		UNION ALL SELECT o.id FROM teaching.org_units o JOIN subtree s ON o.parent_id=s.id
	) SELECT EXISTS(
		SELECT 1 FROM teaching.user_accounts WHERE org_unit_id IN (SELECT id FROM subtree)
		UNION ALL SELECT 1 FROM teaching.role_bindings WHERE scope_org_id IN (SELECT id FROM subtree)
		UNION ALL SELECT 1 FROM teaching.courses WHERE org_unit_id IN (SELECT id FROM subtree)
		UNION ALL SELECT 1 FROM teaching.class_groups WHERE org_unit_id IN (SELECT id FROM subtree)
		UNION ALL SELECT 1 FROM teaching.course_offerings WHERE org_unit_id IN (SELECT id FROM subtree)
	)`, id).Scan(&yes)
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
	rows, err := s.pool.Query(ctx, `SELECT id::text,org_unit_id::text,code,name,description FROM teaching.courses c WHERE ($1='' OR id>$1::uuid) AND ($3 OR teaching.college_of(c.org_unit_id)=ANY($4::uuid[]) OR ($6 AND EXISTS(SELECT 1 FROM teaching.course_offerings o WHERE o.course_id=c.id AND o.teacher_id=$5))) ORDER BY id LIMIT $2`, after, limit+1, p.Has("sys_admin"), p.CollegeScopes(), p.UserID, p.Has("teacher"))
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
	if _, err := s.GetCourse(ctx, p, id); err != nil {
		return Course{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Course{}, fmt.Errorf("begin course update: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := lockAcademicResources(ctx, tx, "course:"+id); err != nil {
		return Course{}, err
	}
	var v Course
	err = tx.QueryRow(ctx, `SELECT id::text,org_unit_id::text,code,name,description FROM teaching.courses WHERE id=$1 FOR UPDATE`, id).Scan(&v.ID, &v.OrgUnitID, &v.Code, &v.Name, &v.Description)
	if err != nil {
		return Course{}, dbError(err)
	}
	if err := requireManageOrg(ctx, tx, p, v.OrgUnitID); err != nil {
		return Course{}, err
	}
	org := v.OrgUnitID
	if in.OrgUnitID != nil {
		org = *in.OrgUnitID
		if org != v.OrgUnitID {
			if err := requireManageOrg(ctx, tx, p, org); err != nil {
				return Course{}, err
			}
			var used bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.course_offerings WHERE course_id=$1)`, id).Scan(&used); err != nil {
				return Course{}, err
			}
			if used {
				return Course{}, apperror.New(http.StatusConflict, "INVALID_STATE", "referenced course ownership cannot change")
			}
		}
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
	err = tx.QueryRow(ctx, `UPDATE teaching.courses SET org_unit_id=$2,code=$3,name=$4,description=$5 WHERE id=$1 RETURNING id::text,org_unit_id::text,code,name,description`, id, org, code, name, description).Scan(&v.ID, &v.OrgUnitID, &v.Code, &v.Name, &v.Description)
	if err != nil {
		return Course{}, dbError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Course{}, dbError(err)
	}
	return v, nil
}

func (s *Service) ListClassGroups(ctx context.Context, p identity.Principal, after string, limit int) ([]ClassGroup, string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,org_unit_id::text,code,name,enrollment_year,expected_size FROM teaching.class_groups g WHERE ($1='' OR id>$1::uuid) AND ($3 OR teaching.college_of(g.org_unit_id)=ANY($4::uuid[]) OR ($6 AND EXISTS(SELECT 1 FROM teaching.course_offerings o WHERE o.class_group_id=g.id AND o.teacher_id=$5))) ORDER BY id LIMIT $2`, after, limit+1, p.Has("sys_admin"), p.CollegeScopes(), p.UserID, p.Has("teacher"))
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
	if _, err := s.GetClassGroup(ctx, p, id); err != nil {
		return ClassGroup{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ClassGroup{}, fmt.Errorf("begin class group update: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := lockAcademicResources(ctx, tx, "class:"+id); err != nil {
		return ClassGroup{}, err
	}
	var v ClassGroup
	err = tx.QueryRow(ctx, `SELECT id::text,org_unit_id::text,code,name,enrollment_year,expected_size FROM teaching.class_groups WHERE id=$1 FOR UPDATE`, id).Scan(&v.ID, &v.OrgUnitID, &v.Code, &v.Name, &v.EnrollmentYear, &v.ExpectedSize)
	if err != nil {
		return ClassGroup{}, dbError(err)
	}
	if err := requireManageOrg(ctx, tx, p, v.OrgUnitID); err != nil {
		return ClassGroup{}, err
	}
	org := v.OrgUnitID
	if in.OrgUnitID != nil {
		org = *in.OrgUnitID
		if org != v.OrgUnitID {
			if err := requireManageOrg(ctx, tx, p, org); err != nil {
				return ClassGroup{}, err
			}
			var used bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.course_offerings WHERE class_group_id=$1)`, id).Scan(&used); err != nil {
				return ClassGroup{}, err
			}
			if used {
				return ClassGroup{}, apperror.New(http.StatusConflict, "INVALID_STATE", "referenced class ownership cannot change")
			}
		}
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
	err = tx.QueryRow(ctx, `UPDATE teaching.class_groups SET org_unit_id=$2,code=$3,name=$4,enrollment_year=$5,expected_size=$6 WHERE id=$1 RETURNING id::text,org_unit_id::text,code,name,enrollment_year,expected_size`, id, org, code, name, year, size).Scan(&v.ID, &v.OrgUnitID, &v.Code, &v.Name, &v.EnrollmentYear, &v.ExpectedSize)
	if err != nil {
		return ClassGroup{}, dbError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ClassGroup{}, dbError(err)
	}
	return v, nil
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
	rows, err := s.pool.Query(ctx, `SELECT id::text,org_unit_id::text,term_id::text,course_id::text,teacher_id::text,class_group_id::text,code,status FROM teaching.course_offerings o WHERE ($1='' OR id>$1::uuid) AND ($3 OR teaching.college_of(o.org_unit_id)=ANY($4::uuid[]) OR ($6 AND o.teacher_id=$5)) ORDER BY id LIMIT $2`, after, limit+1, p.Has("sys_admin"), p.CollegeScopes(), p.UserID, p.Has("teacher"))
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
	if in.Status != "active" && in.Status != "archived" {
		return Offering{}, invalid("invalid offering status")
	}
	if clean(in.Code) == "" {
		return Offering{}, invalid("offering code is required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Offering{}, fmt.Errorf("begin offering creation: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := lockAcademicResources(ctx, tx, "course:"+in.CourseID, "class:"+in.ClassGroupID); err != nil {
		return Offering{}, err
	}
	if err := authorizeOffering(ctx, tx, p, in.OrgUnitID, in.TeacherID); err != nil {
		return Offering{}, err
	}
	var v Offering
	err = tx.QueryRow(ctx, `INSERT INTO teaching.course_offerings(org_unit_id,term_id,course_id,teacher_id,class_group_id,code,status) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text,org_unit_id::text,term_id::text,course_id::text,teacher_id::text,class_group_id::text,code,status`, in.OrgUnitID, in.TermID, in.CourseID, in.TeacherID, in.ClassGroupID, clean(in.Code), in.Status).Scan(&v.ID, &v.OrgUnitID, &v.TermID, &v.CourseID, &v.TeacherID, &v.ClassGroupID, &v.Code, &v.Status)
	if err != nil {
		return Offering{}, dbError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Offering{}, dbError(err)
	}
	return v, nil
}
func (s *Service) PatchOffering(ctx context.Context, p identity.Principal, id string, in PatchOffering) (Offering, error) {
	if err := validatePatchOfferingUUIDs(in); err != nil {
		return Offering{}, err
	}
	if _, err := s.GetOffering(ctx, p, id); err != nil {
		return Offering{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Offering{}, fmt.Errorf("begin offering update: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var v Offering
	err = tx.QueryRow(ctx, `SELECT id::text,org_unit_id::text,term_id::text,course_id::text,teacher_id::text,class_group_id::text,code,status FROM teaching.course_offerings WHERE id=$1 FOR UPDATE`, id).Scan(&v.ID, &v.OrgUnitID, &v.TermID, &v.CourseID, &v.TeacherID, &v.ClassGroupID, &v.Code, &v.Status)
	if err != nil {
		return Offering{}, dbError(err)
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
	if err := lockAcademicResources(ctx, tx, "course:"+v.CourseID, "course:"+course, "class:"+v.ClassGroupID, "class:"+group); err != nil {
		return Offering{}, err
	}
	if err := authorizeOffering(ctx, tx, p, v.OrgUnitID, v.TeacherID); err != nil {
		return Offering{}, err
	}
	if err := authorizeOffering(ctx, tx, p, org, teacher); err != nil {
		return Offering{}, err
	}
	if status != "active" && status != "archived" {
		return Offering{}, invalid("invalid offering status")
	}
	if code == "" {
		return Offering{}, invalid("offering code is required")
	}
	ownershipChanged := !sameUUID(org, v.OrgUnitID) || !sameUUID(term, v.TermID) || !sameUUID(course, v.CourseID) || !sameUUID(teacher, v.TeacherID) || !sameUUID(group, v.ClassGroupID)
	if ownershipChanged {
		var frozen bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.lesson_sessions WHERE offering_id=$1)`, id).Scan(&frozen); err != nil {
			return Offering{}, dbError(err)
		}
		if frozen {
			return Offering{}, apperror.New(http.StatusConflict, "INVALID_STATE", "offering ownership cannot change after a lesson exists")
		}
	}
	teacherChanged, groupChanged := !sameUUID(teacher, v.TeacherID), !sameUUID(group, v.ClassGroupID)
	if teacherChanged || groupChanged {
		rows, err := tx.Query(ctx, `SELECT id FROM teaching.schedule_entries WHERE offering_id=$1 FOR UPDATE`, id)
		if err != nil {
			return Offering{}, dbError(err)
		}
		for rows.Next() {
			var scheduleID string
			if err := rows.Scan(&scheduleID); err != nil {
				rows.Close()
				return Offering{}, dbError(err)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return Offering{}, dbError(err)
		}
		rows.Close()
		if _, err := tx.Exec(ctx, `SET CONSTRAINTS teaching.fk_schedule_offering_resources DEFERRED`); err != nil {
			return Offering{}, dbError(err)
		}
	}
	sets := []string{}
	args := []any{id}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s=$%d", column, len(args)))
	}
	if !sameUUID(org, v.OrgUnitID) {
		add("org_unit_id", org)
	}
	if !sameUUID(term, v.TermID) {
		add("term_id", term)
	}
	if !sameUUID(course, v.CourseID) {
		add("course_id", course)
	}
	if teacherChanged {
		add("teacher_id", teacher)
	}
	if groupChanged {
		add("class_group_id", group)
	}
	if code != v.Code {
		add("code", code)
	}
	if status != v.Status {
		add("status", status)
	}
	if len(sets) > 0 {
		query := `UPDATE teaching.course_offerings SET ` + strings.Join(sets, ",") + ` WHERE id=$1 RETURNING id::text,org_unit_id::text,term_id::text,course_id::text,teacher_id::text,class_group_id::text,code,status`
		if err := tx.QueryRow(ctx, query, args...).Scan(&v.ID, &v.OrgUnitID, &v.TermID, &v.CourseID, &v.TeacherID, &v.ClassGroupID, &v.Code, &v.Status); err != nil {
			return Offering{}, dbError(err)
		}
	}
	if teacherChanged || groupChanged {
		scheduleSets := []string{}
		scheduleArgs := []any{id}
		if teacherChanged {
			scheduleArgs = append(scheduleArgs, teacher)
			scheduleSets = append(scheduleSets, fmt.Sprintf("teacher_id=$%d", len(scheduleArgs)))
		}
		if groupChanged {
			scheduleArgs = append(scheduleArgs, group)
			scheduleSets = append(scheduleSets, fmt.Sprintf("class_group_id=$%d", len(scheduleArgs)))
		}
		if _, err := tx.Exec(ctx, `UPDATE teaching.schedule_entries SET `+strings.Join(scheduleSets, ",")+` WHERE offering_id=$1`, scheduleArgs...); err != nil {
			return Offering{}, dbError(err)
		}
		if _, err := tx.Exec(ctx, `SET CONSTRAINTS teaching.fk_schedule_offering_resources IMMEDIATE`); err != nil {
			return Offering{}, dbError(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Offering{}, dbError(err)
	}
	return v, nil
}

func scanSchedule(row pgx.Row) (Schedule, error) {
	var v Schedule
	err := row.Scan(&v.ID, &v.OfferingID, &v.ClassroomID, &v.StartsAt, &v.EndsAt, &v.Status, &v.TeacherID, &v.ClassGroupID)
	return v, err
}

const scheduleColumns = `s.id::text,s.offering_id::text,s.classroom_id::text,s.starts_at,s.ends_at,s.status,s.teacher_id::text,s.class_group_id::text`

func parseRange(startRaw, endRaw string) (time.Time, time.Time, error) {
	start, err := time.Parse(time.RFC3339, startRaw)
	if err != nil {
		return time.Time{}, time.Time{}, invalid("starts_at must be an RFC3339 date-time")
	}
	end, err := time.Parse(time.RFC3339, endRaw)
	if err != nil {
		return time.Time{}, time.Time{}, invalid("ends_at must be an RFC3339 date-time")
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, invalid("ends_at must be after starts_at")
	}
	return start, end, nil
}

func (s *Service) ListSchedules(ctx context.Context, p identity.Principal, q ScheduleQuery) ([]Schedule, string, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+scheduleColumns+`
		FROM teaching.schedule_entries s JOIN teaching.course_offerings o ON o.id=s.offering_id
		WHERE ($1='' OR s.id>$1::uuid)
		AND ($2='' OR s.teacher_id=$2::uuid) AND ($3='' OR s.classroom_id=$3::uuid) AND ($4='' OR s.class_group_id=$4::uuid)
		AND ($5::timestamptz IS NULL OR (s.status='active' AND s.starts_at<=$5 AND s.ends_at>$5))
		AND ($6::timestamptz IS NULL OR (s.starts_at<$7 AND s.ends_at>$6))
		AND ($8 OR teaching.college_of(o.org_unit_id)=ANY($9::uuid[]) OR ($11 AND o.teacher_id=$10))
		ORDER BY s.id LIMIT $12`, q.After, q.TeacherID, q.ClassroomID, q.ClassGroupID, q.At, q.From, q.To,
		p.Has("sys_admin"), p.CollegeScopes(), p.UserID, p.Has("teacher"), q.Limit+1)
	if err != nil {
		return nil, "", dbError(err)
	}
	defer rows.Close()
	items := []Schedule{}
	for rows.Next() {
		v, err := scanSchedule(rows)
		if err != nil {
			return nil, "", err
		}
		items = append(items, v)
	}
	items, next := nextID(items, q.Limit, func(v Schedule) string { return v.ID })
	return items, next, rows.Err()
}

func (s *Service) GetSchedule(ctx context.Context, p identity.Principal, id string) (Schedule, error) {
	v, err := scanSchedule(s.pool.QueryRow(ctx, `SELECT `+scheduleColumns+`
		FROM teaching.schedule_entries s JOIN teaching.course_offerings o ON o.id=s.offering_id
		WHERE s.id=$1 AND ($2 OR teaching.college_of(o.org_unit_id)=ANY($3::uuid[]) OR ($5 AND o.teacher_id=$4))`,
		id, p.Has("sys_admin"), p.CollegeScopes(), p.UserID, p.Has("teacher")))
	if errors.Is(err, pgx.ErrNoRows) {
		return Schedule{}, notFound("schedule")
	}
	return v, dbError(err)
}

func (s *Service) CreateSchedule(ctx context.Context, p identity.Principal, in CreateSchedule) (Schedule, error) {
	start, end, err := parseRange(in.StartsAt, in.EndsAt)
	if err != nil {
		return Schedule{}, err
	}
	if in.Status != "active" && in.Status != "cancelled" {
		return Schedule{}, invalid("invalid schedule status")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Schedule{}, fmt.Errorf("begin schedule creation: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	v, err := createScheduleTx(ctx, tx, p, in.OfferingID, in.ClassroomID, start, end, in.Status, 0)
	if err != nil {
		return Schedule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Schedule{}, scheduleDBError(err, 0, start, end)
	}
	return v, nil
}

func createScheduleTx(ctx context.Context, tx pgx.Tx, p identity.Principal, offeringID, classroomID string, start, end time.Time, status string, rowNumber int) (Schedule, error) {
	var org, teacher, group, offeringStatus string
	err := tx.QueryRow(ctx, `SELECT org_unit_id::text,teacher_id::text,class_group_id::text,status FROM teaching.course_offerings WHERE id=$1 FOR SHARE`, offeringID).Scan(&org, &teacher, &group, &offeringStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Schedule{}, invalid("offering_id does not exist")
	}
	if err != nil {
		return Schedule{}, dbError(err)
	}
	if offeringStatus != "active" {
		return Schedule{}, apperror.New(http.StatusConflict, "INVALID_STATE", "schedules require an active offering")
	}
	if err := requireManageOrg(ctx, tx, p, org); err != nil {
		return Schedule{}, err
	}
	if err := lockAcademicResources(ctx, tx, "teacher:"+teacher, "class:"+group, "room:"+classroomID); err != nil {
		return Schedule{}, err
	}
	v, err := scanSchedule(tx.QueryRow(ctx, `INSERT INTO teaching.schedule_entries AS s(offering_id,teacher_id,class_group_id,classroom_id,starts_at,ends_at,status)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+scheduleColumns, offeringID, teacher, group, classroomID, start, end, status))
	if err != nil {
		return Schedule{}, scheduleDBError(err, rowNumber, start, end)
	}
	return v, nil
}

func (s *Service) PatchSchedule(ctx context.Context, p identity.Principal, id string, in PatchSchedule) (Schedule, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Schedule{}, fmt.Errorf("begin schedule update: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// Read the association first, then take offering locks before the schedule row.
	// Classroom creation uses the same offering -> schedule order. If another
	// schedule update changes the association before our row lock, reject this
	// stale attempt instead of acquiring a newly discovered offering out of order.
	initial, err := scanSchedule(tx.QueryRow(ctx, `SELECT `+scheduleColumns+` FROM teaching.schedule_entries s WHERE s.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Schedule{}, notFound("schedule")
	}
	if err != nil {
		return Schedule{}, dbError(err)
	}
	offering := initial.OfferingID
	if in.OfferingID != nil {
		offering = *in.OfferingID
	}
	offeringIDs := []string{initial.OfferingID}
	if !sameUUID(offering, initial.OfferingID) {
		offeringIDs = append(offeringIDs, offering)
	}
	sort.Strings(offeringIDs)
	rows, err := tx.Query(ctx, `SELECT id::text FROM teaching.course_offerings WHERE id=ANY($1::uuid[]) ORDER BY id FOR SHARE`, offeringIDs)
	if err != nil {
		return Schedule{}, dbError(err)
	}
	lockedOfferings := 0
	for rows.Next() {
		lockedOfferings++
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Schedule{}, dbError(err)
	}
	if lockedOfferings != len(offeringIDs) {
		return Schedule{}, invalid("offering_id does not exist")
	}
	current, err := scanSchedule(tx.QueryRow(ctx, `SELECT `+scheduleColumns+` FROM teaching.schedule_entries s WHERE s.id=$1 FOR UPDATE`, id))
	if err != nil {
		return Schedule{}, dbError(err)
	}
	if !sameUUID(current.OfferingID, initial.OfferingID) {
		return Schedule{}, apperror.New(http.StatusConflict, "REVISION_CONFLICT", "schedule association changed concurrently")
	}
	classroom, status := current.ClassroomID, current.Status
	start, end := current.StartsAt, current.EndsAt
	if in.ClassroomID != nil {
		classroom = *in.ClassroomID
	}
	if in.Status != nil {
		status = *in.Status
	}
	if in.StartsAt != nil {
		start, err = time.Parse(time.RFC3339, *in.StartsAt)
		if err != nil {
			return Schedule{}, invalid("starts_at must be an RFC3339 date-time")
		}
	}
	if in.EndsAt != nil {
		end, err = time.Parse(time.RFC3339, *in.EndsAt)
		if err != nil {
			return Schedule{}, invalid("ends_at must be an RFC3339 date-time")
		}
	}
	if !end.After(start) {
		return Schedule{}, invalid("ends_at must be after starts_at")
	}
	if status != "active" && status != "cancelled" {
		return Schedule{}, invalid("invalid schedule status")
	}
	var oldOrg, newOrg, teacher, group string
	if err := tx.QueryRow(ctx, `SELECT org_unit_id::text FROM teaching.course_offerings WHERE id=$1`, current.OfferingID).Scan(&oldOrg); err != nil {
		return Schedule{}, dbError(err)
	}
	err = tx.QueryRow(ctx, `SELECT org_unit_id::text,teacher_id::text,class_group_id::text FROM teaching.course_offerings WHERE id=$1`, offering).Scan(&newOrg, &teacher, &group)
	if errors.Is(err, pgx.ErrNoRows) {
		return Schedule{}, invalid("offering_id does not exist")
	}
	if err != nil {
		return Schedule{}, dbError(err)
	}
	if err := requireManageOrg(ctx, tx, p, oldOrg); err != nil {
		return Schedule{}, err
	}
	if err := requireManageOrg(ctx, tx, p, newOrg); err != nil {
		return Schedule{}, err
	}
	if err := lockAcademicResources(ctx, tx,
		"teacher:"+current.TeacherID, "class:"+current.ClassGroupID, "room:"+current.ClassroomID,
		"teacher:"+teacher, "class:"+group, "room:"+classroom); err != nil {
		return Schedule{}, err
	}
	if !sameUUID(offering, current.OfferingID) || !sameUUID(classroom, current.ClassroomID) || !start.Equal(current.StartsAt) || !end.Equal(current.EndsAt) {
		var frozen bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.lesson_sessions WHERE schedule_entry_id=$1)`, id).Scan(&frozen); err != nil {
			return Schedule{}, dbError(err)
		}
		if frozen {
			return Schedule{}, apperror.New(http.StatusConflict, "INVALID_STATE", "a schedule linked to a lesson cannot change its assignment or time")
		}
	}
	v, err := scanSchedule(tx.QueryRow(ctx, `UPDATE teaching.schedule_entries AS s SET offering_id=$2,teacher_id=$3,class_group_id=$4,classroom_id=$5,starts_at=$6,ends_at=$7,status=$8 WHERE id=$1 RETURNING `+scheduleColumns,
		id, offering, teacher, group, classroom, start, end, status))
	if err != nil {
		return Schedule{}, scheduleDBError(err, 0, start, end)
	}
	if err := tx.Commit(ctx); err != nil {
		return Schedule{}, scheduleDBError(err, 0, start, end)
	}
	return v, nil
}

func (s *Service) ImportSchedules(ctx context.Context, p identity.Principal, in ImportSchedules) (ImportResult, error) {
	if len(in.Rows) < 1 || len(in.Rows) > 1000 {
		return ImportResult{}, invalid("rows must contain between 1 and 1000 schedules")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ImportResult{}, fmt.Errorf("begin schedule import: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	result := ImportResult{Items: make([]Schedule, 0, len(in.Rows))}
	for index, row := range in.Rows {
		start, end, err := parseRange(row.StartsAt, row.EndsAt)
		if err != nil {
			return ImportResult{}, apperror.WithDetails(http.StatusBadRequest, "INVALID_ARGUMENT", "schedule import row is invalid", map[string]any{"row_numbers": []int{index + 1}})
		}
		v, err := createScheduleTx(ctx, tx, p, row.OfferingID, row.ClassroomID, start, end, "active", index+1)
		if err != nil {
			return ImportResult{}, err
		}
		result.Items = append(result.Items, v)
	}
	if err := tx.Commit(ctx); err != nil {
		return ImportResult{}, dbError(err)
	}
	return result, nil
}

func scheduleDBError(err error, rowNumber int, start, end time.Time) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23P01" {
		resource := "resource"
		switch pgErr.ConstraintName {
		case "ex_schedule_teacher":
			resource = "teacher"
		case "ex_schedule_class":
			resource = "class_group"
		case "ex_schedule_room":
			resource = "classroom"
		}
		details := map[string]any{"resource_type": resource, "conflict_start": start.Format(time.RFC3339), "conflict_end": end.Format(time.RFC3339)}
		if rowNumber > 0 {
			details["row_numbers"] = []int{rowNumber}
		} else {
			details["row_numbers"] = []int{}
		}
		return apperror.WithDetails(http.StatusConflict, "SCHEDULE_CONFLICT", "schedule conflicts with an existing resource booking", details)
	}
	return dbError(err)
}

func sameUUID(a, b string) bool {
	var left, right pgtype.UUID
	if err := left.Scan(a); err != nil {
		return false
	}
	if err := right.Scan(b); err != nil {
		return false
	}
	return left == right
}

func (s *Service) authorizeOffering(ctx context.Context, p identity.Principal, org, teacher string) error {
	return authorizeOffering(ctx, s.pool, p, org, teacher)
}
func authorizeOffering(ctx context.Context, q rowGetter, p identity.Principal, org, teacher string) error {
	ok, err := canManageOrg(ctx, q, p, org)
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
	err = q.QueryRow(ctx, `SELECT teaching.college_of(u.org_unit_id)=teaching.college_of($1) FROM teaching.user_accounts u WHERE u.id=$2`, org, teacher).Scan(&same)
	if errors.Is(err, pgx.ErrNoRows) || !same {
		return apperror.New(http.StatusForbidden, "FORBIDDEN", "academic administrators cannot assign a cross-college teacher")
	}
	return err
}

func requireManageOrg(ctx context.Context, q rowGetter, p identity.Principal, orgID string) error {
	ok, err := canManageOrg(ctx, q, p, orgID)
	if err != nil {
		return err
	}
	if !ok {
		return apperror.New(http.StatusForbidden, "FORBIDDEN", "organization is outside your scope")
	}
	return nil
}

func lockAcademicResources(ctx context.Context, tx pgx.Tx, keys ...string) error {
	unique := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		unique[key] = struct{}{}
	}
	ordered := make([]string, 0, len(unique))
	for key := range unique {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "academic-resource:"+key); err != nil {
			return fmt.Errorf("lock academic resource: %w", err)
		}
	}
	return nil
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
		case "23P01":
			return apperror.New(http.StatusConflict, "SCHEDULE_CONFLICT", "schedule conflicts with an existing resource booking")
		case "23503", "23514", "22P02":
			return invalid("related resource or field is invalid")
		}
	}
	return fmt.Errorf("academic storage: %w", err)
}
