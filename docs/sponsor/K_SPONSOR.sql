-- ============================================================================
-- Kairo 投喂作者排行榜表 (v0.14)
-- ============================================================================
--
-- 表名: K_SPONSOR
-- serviceID: KairoSponsorLeaderboardAction
--   (在 dispatcher 配置, Java 反射按 serviceID 路由到这个 Action)
--
-- 数据流程:
--   1) 同事请喝咖啡 → 维护方手工 / 简单录入到 K_SPONSOR
--   2) 投喂作者页 → Go 端 /api/sponsor/leaderboard → Java 端
--      /credit/httpInterface?serviceID=KairoSponsorLeaderboardAction
--   3) Java 端查 K_SPONSOR 按 TOTAL 倒序排, ROW_NUMBER() 算 rank
--   4) 返前 50 条给 Go 端 → 拼上前端写死的 50 个昵称 (NICKNAMES) → 给前端渲染
--
-- 字段命名来源 (跟用户原话一致):
--   库迪 = cotti (COTTI 字段)
--   瑞幸 = LUCKY  (用户原话: "瑞幸是LUCKY")
--   奶茶 = milktea (用户原话: "random 就是奶茶啊 随机奶茶数量")
--
-- 不需要 K_AUDIT: 排行榜是只读查询, 没有"敏感事件"需要审计
-- ============================================================================

-- 主表
CREATE TABLE K_SPONSOR (
    ID         NUMBER        NOT NULL,
    REAL_NAME  VARCHAR2(100) NOT NULL,         -- 真实姓名
    COTTI      NUMBER        DEFAULT 0 NOT NULL,-- 库迪杯数
    LUCKY      NUMBER        DEFAULT 0 NOT NULL,-- 瑞幸杯数
    MILKTEA    NUMBER        DEFAULT 0 NOT NULL,-- 奶茶杯数
    TOTAL      NUMBER        DEFAULT 0 NOT NULL,-- 总杯数 (冗余存, 触发器维护)
    CREATED_AT DATE          DEFAULT SYSDATE,   -- 首次赞助日期
    UPDATED_AT DATE          DEFAULT SYSDATE,   -- 最后更新时间
    CONSTRAINT PK_K_SPONSOR PRIMARY KEY (ID)
);

COMMENT ON TABLE K_SPONSOR IS 'Kairo 投喂作者排行榜 (v0.14, 跟 K_ACT_CODE 同款命名风格)';
COMMENT ON COLUMN K_SPONSOR.ID         IS '主键, SEQ_K_SPONSOR.NEXTVAL';
COMMENT ON COLUMN K_SPONSOR.REAL_NAME  IS '真实姓名, 投喂者本人';
COMMENT ON COLUMN K_SPONSOR.COTTI      IS '库迪咖啡杯数 (cotti = 库迪官方英文)';
COMMENT ON COLUMN K_SPONSOR.LUCKY      IS '瑞幸咖啡杯数 (用户原话: 瑞幸是 LUCKY)';
COMMENT ON COLUMN K_SPONSOR.MILKTEA    IS '奶茶杯数 (用户原话: random 就是奶茶)';
COMMENT ON COLUMN K_SPONSOR.TOTAL      IS '总杯数 = COTTI+LUCKY+MILKTEA, 触发器自动维护, 方便 ORDER BY';
COMMENT ON COLUMN K_SPONSOR.CREATED_AT IS '首次赞助日期, 触发器填';
COMMENT ON COLUMN K_SPONSOR.UPDATED_AT IS '最后更新时间, 触发器每次改杯数时刷新';

-- 序列
CREATE SEQUENCE SEQ_K_SPONSOR START WITH 1 INCREMENT BY 1 NOCACHE NOCYCLE;

-- 索引: 按 TOTAL 倒序查排行榜
CREATE INDEX IDX_K_SPONSOR_TOTAL ON K_SPONSOR (TOTAL DESC, ID ASC);

-- 触发器 1: INSERT/UPDATE 时自动算 TOTAL + UPDATED_AT
CREATE OR REPLACE TRIGGER TRG_K_SPONSOR_TOTAL
BEFORE INSERT OR UPDATE OF COTTI, LUCKY, MILKTEA ON K_SPONSOR
FOR EACH ROW
BEGIN
    :NEW.TOTAL     := NVL(:NEW.COTTI, 0) + NVL(:NEW.LUCKY, 0) + NVL(:NEW.MILKTEA, 0);
    :NEW.UPDATED_AT := SYSDATE;
END;
/

-- 触发器 2: INSERT 时自动填 CREATED_AT (如果调用方没传)
CREATE OR REPLACE TRIGGER TRG_K_SPONSOR_CREATED
BEFORE INSERT ON K_SPONSOR
FOR EACH ROW
WHEN (NEW.CREATED_AT IS NULL)
BEGIN
    :NEW.CREATED_AT := SYSDATE;
END;
/

-- 触发器 3: INSERT 时自动从序列拿 ID
CREATE OR REPLACE TRIGGER TRG_K_SPONSOR_ID
BEFORE INSERT ON K_SPONSOR
FOR EACH ROW
WHEN (NEW.ID IS NULL)
BEGIN
    :NEW.ID := SEQ_K_SPONSOR.NEXTVAL;
END;
/

-- ============================================================================
-- 录入示例 (3 条, 跟 mock-sponsor-server 预置数据 1:1 对齐, 方便开发期联调)
-- ============================================================================
INSERT INTO K_SPONSOR (REAL_NAME, COTTI, LUCKY, MILKTEA) VALUES ('张三', 4, 4, 1);
INSERT INTO K_SPONSOR (REAL_NAME, COTTI, LUCKY, MILKTEA) VALUES ('李四', 3, 4, 1);
INSERT INTO K_SPONSOR (REAL_NAME, COTTI, LUCKY, MILKTEA) VALUES ('王五', 2, 3, 1);
INSERT INTO K_SPONSOR (REAL_NAME, COTTI, LUCKY, MILKTEA) VALUES ('赵六', 0, 4, 0);
INSERT INTO K_SPONSOR (REAL_NAME, COTTI, LUCKY, MILKTEA) VALUES ('钱七', 3, 2, 1);
COMMIT;

-- 验证: 应该看到 5 条, 按 TOTAL 倒序, 1-5 名是 张三(9) / 李四(8) / 王五(6) / 钱七(6) / 赵六(4)
-- 钱七 (ID=5) 比王五 (ID=3) 后入, 同分时按 ID 升序排后面
SELECT REAL_NAME, COTTI, LUCKY, MILKTEA, TOTAL FROM K_SPONSOR ORDER BY TOTAL DESC, ID ASC;
