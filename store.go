package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Asset 是分发所需的最小投影（与 console assets 表对齐，只读）。
type Asset struct {
	GLBPath   string
	GLBHash   string
	Thumbnail string
	Renders   []string
}

// ErrNotFound：资产不存在（404 语义的单一来源）。
var ErrNotFound = errors.New("asset not found")

// AssetStore：asset_id → 存储键映射（生产为 console 同库只读）。
type AssetStore interface {
	GetAsset(ctx context.Context, assetID string) (*Asset, error)
}

// PGStore：console PostgreSQL 只读连接。
type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(ctx context.Context, databaseURL string) (*PGStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("pg pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg ping: %w", err)
	}
	return &PGStore{pool: pool}, nil
}

func (s *PGStore) Close() { s.pool.Close() }

func (s *PGStore) GetAsset(ctx context.Context, assetID string) (*Asset, error) {
	var (
		glbPath, glbHash, thumbnail *string
		rendersRaw                  []byte
	)
	err := s.pool.QueryRow(ctx,
		`SELECT glb_path, glb_hash, thumbnail, renders FROM assets WHERE asset_id = $1`, assetID,
	).Scan(&glbPath, &glbHash, &thumbnail, &rendersRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	asset := &Asset{}
	if glbPath != nil {
		asset.GLBPath = *glbPath
	}
	if glbHash != nil {
		asset.GLBHash = *glbHash
	}
	if thumbnail != nil {
		asset.Thumbnail = *thumbnail
	}
	if len(rendersRaw) > 0 {
		if err := json.Unmarshal(rendersRaw, &asset.Renders); err != nil {
			return nil, fmt.Errorf("renders json: %w", err)
		}
	}
	return asset, nil
}

// MapStore：测试/本地替身。
type MapStore map[string]*Asset

func (m MapStore) GetAsset(_ context.Context, assetID string) (*Asset, error) {
	a, ok := m[assetID]
	if !ok {
		return nil, ErrNotFound
	}
	return a, nil
}
