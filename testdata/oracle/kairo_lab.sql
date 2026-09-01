-- Kairo lab schema: 11g-compatible, also runs on 21c XE PDB XEPDB1.
-- Connect as SYSTEM inside the PDB, then: @kairo_lab.sql

WHENEVER SQLERROR CONTINUE
DROP USER kairo_lab CASCADE;
DROP USER kairo_ro CASCADE;
WHENEVER SQLERROR EXIT SQL.SQLCODE

CREATE USER kairo_lab IDENTIFIED BY "KairoLab21#"
  DEFAULT TABLESPACE users
  TEMPORARY TABLESPACE temp
  QUOTA UNLIMITED ON users;
GRANT CONNECT, RESOURCE, CREATE VIEW, CREATE PROCEDURE, CREATE TRIGGER TO kairo_lab;

CREATE USER kairo_ro IDENTIFIED BY "KairoLab21#"
  DEFAULT TABLESPACE users
  TEMPORARY TABLESPACE temp;
GRANT CONNECT TO kairo_ro;

CREATE TABLE kairo_lab.departments (
  id NUMBER(10) PRIMARY KEY,
  code VARCHAR2(20) NOT NULL,
  name VARCHAR2(80) NOT NULL,
  city VARCHAR2(40)
);

CREATE TABLE kairo_lab.employees (
  id NUMBER(10) PRIMARY KEY,
  employee_no VARCHAR2(20) NOT NULL,
  display_name VARCHAR2(80) NOT NULL,
  department_id NUMBER(10) NOT NULL,
  monthly_salary NUMBER(10,2),
  active NUMBER(1) DEFAULT 1 NOT NULL,
  tags VARCHAR2(400),
  remark VARCHAR2(4000),
  hired_at DATE,
  CONSTRAINT fk_emp_dept FOREIGN KEY (department_id) REFERENCES kairo_lab.departments(id),
  CONSTRAINT ck_emp_active CHECK (active IN (0,1))
);

CREATE UNIQUE INDEX kairo_lab.ux_emp_no ON kairo_lab.employees(employee_no);
CREATE INDEX kairo_lab.ix_emp_dept ON kairo_lab.employees(department_id);
CREATE INDEX kairo_lab.ix_emp_hired ON kairo_lab.employees(hired_at);

CREATE TABLE kairo_lab.audit_events (
  id NUMBER(10) PRIMARY KEY,
  employee_id NUMBER(10),
  event_type VARCHAR2(40) NOT NULL,
  payload VARCHAR2(4000),
  created_at DATE DEFAULT SYSDATE,
  CONSTRAINT fk_audit_emp FOREIGN KEY (employee_id) REFERENCES kairo_lab.employees(id)
);
CREATE INDEX kairo_lab.ix_audit_emp ON kairo_lab.audit_events(employee_id, created_at);

INSERT INTO kairo_lab.departments(id, code, name, city)
SELECT LEVEL,
       'D' || LPAD(LEVEL, 2, '0'),
       CASE MOD(LEVEL, 6)
         WHEN 0 THEN '生产支持'
         WHEN 1 THEN '信贷核心'
         WHEN 2 THEN '批处理'
         WHEN 3 THEN '运维'
         WHEN 4 THEN '风控'
         ELSE '数据平台'
       END || LEVEL,
       CASE MOD(LEVEL, 3) WHEN 0 THEN '上海' WHEN 1 THEN '北京' ELSE '深圳' END
FROM dual CONNECT BY LEVEL <= 12;

INSERT INTO kairo_lab.employees (
  id, employee_no, display_name, department_id, monthly_salary, active, tags, remark, hired_at
)
SELECT LEVEL,
       'E' || LPAD(LEVEL, 5, '0'),
       CASE MOD(LEVEL, 7)
         WHEN 0 THEN '张三'
         WHEN 1 THEN '李四'
         WHEN 2 THEN '王五'
         WHEN 3 THEN '赵六'
         WHEN 4 THEN '陈七'
         WHEN 5 THEN '周八'
         ELSE '吴九'
       END || LEVEL,
       MOD(LEVEL - 1, 12) + 1,
       8000 + MOD(LEVEL * 17, 25000),
       CASE WHEN MOD(LEVEL, 17) = 0 THEN 0 ELSE 1 END,
       CASE MOD(LEVEL, 4)
         WHEN 0 THEN '["Oracle 11g","AIX"]'
         WHEN 1 THEN '["值班"]'
         WHEN 2 THEN '["MySQL","Redis"]'
         ELSE '[]'
       END,
       CASE WHEN MOD(LEVEL, 11) = 0 THEN NULL ELSE '中文备注：生产变更窗口 #' || LEVEL END,
       DATE '2018-01-01' + MOD(LEVEL, 2500)
FROM dual CONNECT BY LEVEL <= 2500;

INSERT INTO kairo_lab.audit_events(id, employee_id, event_type, payload, created_at)
SELECT LEVEL,
       MOD(LEVEL - 1, 2500) + 1,
       CASE MOD(LEVEL, 5)
         WHEN 0 THEN 'LOGIN'
         WHEN 1 THEN 'QUERY'
         WHEN 2 THEN 'EXPORT'
         WHEN 3 THEN 'EXPLAIN'
         ELSE 'SCAN'
       END,
       CASE WHEN MOD(LEVEL, 23) = 0 THEN NULL ELSE '{"seq":' || LEVEL || ',"ok":true}' END,
       SYSDATE - MOD(LEVEL, 90)
FROM dual CONNECT BY LEVEL <= 8000;

CREATE OR REPLACE VIEW kairo_lab.employee_directory AS
SELECT e.id, e.employee_no, e.display_name, d.name AS department_name,
       e.monthly_salary, e.active, e.remark
FROM kairo_lab.employees e
JOIN kairo_lab.departments d ON d.id = e.department_id;

CREATE OR REPLACE FUNCTION kairo_lab.annual_salary(p_monthly NUMBER)
RETURN NUMBER
IS
BEGIN
  RETURN NVL(p_monthly, 0) * 12;
END;
/

CREATE OR REPLACE PROCEDURE kairo_lab.list_department(p_id IN NUMBER, p_count OUT NUMBER)
IS
BEGIN
  SELECT COUNT(*) INTO p_count FROM kairo_lab.employees WHERE department_id = p_id;
END;
/

CREATE OR REPLACE TRIGGER kairo_lab.trg_emp_no
BEFORE INSERT ON kairo_lab.employees
FOR EACH ROW
WHEN (new.employee_no IS NULL)
BEGIN
  :new.employee_no := 'E' || LPAD(:new.id, 5, '0');
END;
/

GRANT SELECT ON kairo_lab.departments TO kairo_ro;
GRANT SELECT ON kairo_lab.employees TO kairo_ro;
GRANT SELECT ON kairo_lab.audit_events TO kairo_ro;
GRANT SELECT ON kairo_lab.employee_directory TO kairo_ro;
GRANT EXECUTE ON kairo_lab.annual_salary TO kairo_ro;

COMMIT;
