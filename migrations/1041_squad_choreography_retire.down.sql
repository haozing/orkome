-- down：恢复布尔列（存量行回填 false=派发式，退役前的默认值）。
ALTER TABLE squad ADD COLUMN choreography boolean NOT NULL DEFAULT false;
