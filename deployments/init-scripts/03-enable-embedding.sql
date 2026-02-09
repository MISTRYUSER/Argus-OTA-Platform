-- ============================================================================
-- Migration: Enable Embedding for RAG
-- Version: 2.2
-- Description: 启用 ai_diagnoses 表的 embedding 字段和向量索引
-- ============================================================================

-- Step 1: 添加 embedding 字段（如果不存在）
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'ai_diagnoses' AND column_name = 'embedding'
    ) THEN
        ALTER TABLE ai_diagnoses
        ADD COLUMN embedding VECTOR(1536);

        RAISE NOTICE 'embedding column added to ai_diagnoses';
    ELSE
        RAISE NOTICE 'embedding column already exists in ai_diagnoses';
    END IF;
END $$;

-- Step 2: 创建向量索引（HNSW，用于近似最近邻搜索）
DROP INDEX IF EXISTS idx_diagnoses_embedding_hnsw;

CREATE INDEX idx_diagnoses_embedding_hnsw
    ON ai_diagnoses
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

COMMENT ON INDEX idx_diagnoses_embedding_hnsw IS 'HNSW 索引：用于 RAG 向量相似度搜索（余弦相似度）';

-- Step 3: 验证
SELECT
    'pgvector extension' AS check_item,
    extname AS status
FROM pg_extension
WHERE extname = 'vector'

UNION ALL

SELECT
    'embedding column' AS check_item,
    column_name AS status
FROM information_schema.columns
WHERE table_name = 'ai_diagnoses' AND column_name = 'embedding'

UNION ALL

SELECT
    'hnsw index' AS check_item,
    indexname AS status
FROM pg_indexes
WHERE tablename = 'ai_diagnoses' AND indexname = 'idx_diagnoses_embedding_hnsw';

-- ============================================================================
-- 说明
-- ============================================================================
-- 1. VECTOR(1536): OpenAI text-embedding-ada-002 的维度
-- 2. vector_cosine_ops: 余弦相似度操作符（适用于归一化向量）
-- 3. HNSW 索引参数：
--    - m = 16: 每个节点的最大连接数（默认 16）
--    - ef_construction = 64: 构建索引时的搜索范围（默认 64）
--
-- 性能对比：
-- - 不带索引：O(n) 线性扫描（10 万条记录约 1 秒）
-- - HNSW 索引：O(log n) 近似搜索（10 万条记录约 10 毫秒）
-- ============================================================================
