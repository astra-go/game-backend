-- ============================================
-- Astra Game Backend - 数据库初始化脚本
-- ============================================

-- 创建数据库（如果不存在）
CREATE DATABASE IF NOT EXISTS astra_game
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_unicode_ci;

USE astra_game;

-- ============================================
-- 玩家表
-- ============================================
CREATE TABLE IF NOT EXISTS players (
  id VARCHAR(64) PRIMARY KEY COMMENT '玩家ID',
  username VARCHAR(32) UNIQUE NOT NULL COMMENT '用户名',
  password_hash VARCHAR(64) NOT NULL COMMENT '密码哈希',
  level INT DEFAULT 1 COMMENT '等级',
  exp BIGINT DEFAULT 0 COMMENT '经验值',
  gold BIGINT DEFAULT 1000 COMMENT '金币',
  diamond BIGINT DEFAULT 100 COMMENT '钻石',
  mmr INT DEFAULT 1000 COMMENT 'MMR分数',
  elo INT DEFAULT 1000 COMMENT 'ELO分数',
  win_count INT DEFAULT 0 COMMENT '胜利次数',
  lose_count INT DEFAULT 0 COMMENT '失败次数',
  last_login_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP COMMENT '最后登录时间',
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  INDEX idx_username (username),
  INDEX idx_mmr (mmr),
  INDEX idx_elo (elo)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='玩家表';

-- ============================================
-- 房间表
-- ============================================
CREATE TABLE IF NOT EXISTS rooms (
  id VARCHAR(64) PRIMARY KEY COMMENT '房间ID',
  name VARCHAR(128) NOT NULL COMMENT '房间名称',
  owner_id VARCHAR(64) NOT NULL COMMENT '房主ID',
  status ENUM('waiting', 'playing', 'ended') DEFAULT 'waiting' COMMENT '房间状态',
  max_players INT DEFAULT 10 COMMENT '最大玩家数',
  current_tick BIGINT DEFAULT 0 COMMENT '当前帧号',
  map_id INT DEFAULT 1 COMMENT '地图ID',
  mode VARCHAR(32) DEFAULT 'casual' COMMENT '游戏模式',
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  ended_at TIMESTAMP NULL COMMENT '结束时间',
  INDEX idx_owner (owner_id),
  INDEX idx_status (status),
  INDEX idx_mode (mode),
  FOREIGN KEY (owner_id) REFERENCES players(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='房间表';

-- ============================================
-- 房间成员表
-- ============================================
CREATE TABLE IF NOT EXISTS room_members (
  id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '自增ID',
  room_id VARCHAR(64) NOT NULL COMMENT '房间ID',
  player_id VARCHAR(64) NOT NULL COMMENT '玩家ID',
  team_id INT DEFAULT 0 COMMENT '队伍ID',
  role VARCHAR(32) DEFAULT 'member' COMMENT '角色',
  hero_id INT DEFAULT 0 COMMENT '英雄ID',
  mmr INT DEFAULT 1000 COMMENT 'MMR分数',
  is_online BOOLEAN DEFAULT TRUE COMMENT '是否在线',
  last_heartbeat BIGINT DEFAULT 0 COMMENT '最后心跳时间',
  quit_at TIMESTAMP NULL COMMENT '退出时间',
  joined_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP COMMENT '加入时间',
  UNIQUE KEY uk_room_player (room_id, player_id),
  INDEX idx_room (room_id),
  INDEX idx_player (player_id),
  FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE,
  FOREIGN KEY (player_id) REFERENCES players(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='房间成员表';

-- ============================================
-- 游戏会话表
-- ============================================
CREATE TABLE IF NOT EXISTS game_sessions (
  id VARCHAR(64) PRIMARY KEY COMMENT '会话ID',
  room_id VARCHAR(64) NOT NULL COMMENT '房间ID',
  player_id VARCHAR(64) NOT NULL COMMENT '玩家ID',
  start_frame BIGINT DEFAULT 0 COMMENT '开始帧',
  end_frame BIGINT DEFAULT 0 COMMENT '结束帧',
  is_active BOOLEAN DEFAULT TRUE COMMENT '是否活跃',
  reconnect_token VARCHAR(64) UNIQUE COMMENT '重连令牌',
  last_heartbeat BIGINT DEFAULT 0 COMMENT '最后心跳',
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  INDEX idx_room (room_id),
  INDEX idx_player (player_id),
  INDEX idx_active (is_active),
  INDEX idx_token (reconnect_token),
  FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE,
  FOREIGN KEY (player_id) REFERENCES players(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='游戏会话表';

-- ============================================
-- 匹配历史表
-- ============================================
CREATE TABLE IF NOT EXISTS match_history (
  id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '自增ID',
  player_id VARCHAR(64) NOT NULL COMMENT '玩家ID',
  room_id VARCHAR(64) NOT NULL COMMENT '房间ID',
  mode VARCHAR(32) NOT NULL COMMENT '游戏模式',
  mmr_before INT DEFAULT 1000 COMMENT '匹配前MMR',
  mmr_after INT DEFAULT 1000 COMMENT '匹配后MMR',
  is_win BOOLEAN DEFAULT FALSE COMMENT '是否胜利',
  wait_time INT DEFAULT 0 COMMENT '匹配等待时间(秒)',
  duration INT DEFAULT 0 COMMENT '游戏时长(秒)',
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  INDEX idx_player (player_id),
  INDEX idx_room (room_id),
  INDEX idx_mode (mode),
  INDEX idx_created (created_at),
  FOREIGN KEY (player_id) REFERENCES players(id) ON DELETE CASCADE,
  FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='匹配历史表';

-- ============================================
-- 游戏记录表（战绩详情）
-- ============================================
CREATE TABLE IF NOT EXISTS game_records (
  id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '自增ID',
  session_id VARCHAR(64) NOT NULL COMMENT '会话ID',
  player_id VARCHAR(64) NOT NULL COMMENT '玩家ID',
  kills INT DEFAULT 0 COMMENT '击杀数',
  deaths INT DEFAULT 0 COMMENT '死亡数',
  assists INT DEFAULT 0 COMMENT '助攻数',
  score INT DEFAULT 0 COMMENT '得分',
  damage_dealt BIGINT DEFAULT 0 COMMENT '造成伤害',
  damage_taken BIGINT DEFAULT 0 COMMENT '承受伤害',
  healing_done BIGINT DEFAULT 0 COMMENT '治疗量',
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  UNIQUE KEY uk_session_player (session_id, player_id),
  INDEX idx_session (session_id),
  INDEX idx_player (player_id),
  FOREIGN KEY (session_id) REFERENCES game_sessions(id) ON DELETE CASCADE,
  FOREIGN KEY (player_id) REFERENCES players(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='游戏记录表';

-- ============================================
-- 插入测试数据
-- ============================================

-- 插入测试玩家（密码: test123）
-- 密码哈希: bcrypt hash of "test123"
INSERT INTO players (id, username, password_hash, level, mmr, elo, win_count, lose_count) VALUES
  ('player_1001', 'testplayer1', '$2a$10$8K1p/a0dL3LzWPVFZ0BWPuVx3qHrDlOK/xZB/Bz7QGWD.yGvw1Qa', 10, 1200, 1200, 15, 10),
  ('player_1002', 'testplayer2', '$2a$10$8K1p/a0dL3LzWPVFZ0BWPuVx3qHrDlOK/xZB/Bz7QGWD.yGvw1Qa', 8, 1100, 1100, 12, 8),
  ('player_1003', 'testplayer3', '$2a$10$8K1p/a0dL3LzWPVFZ0BWPuVx3qHrDlOK/xZB/Bz7QGWD.yGvw1Qa', 5, 1050, 1050, 8, 12),
  ('player_1004', 'testplayer4', '$2a$10$8K1p/a0dL3LzWPVFZ0BWPuVx3qHrDlOK/xZB/Bz7QGWD.yGvw1Qa', 12, 1300, 1300, 20, 5),
  ('player_1005', 'testplayer5', '$2a$10$8K1p/a0dL3LzWPVFZ0BWPuVx3qHrDlOK/xZB/Bz7QGWD.yGvw1Qa', 3, 950, 950, 5, 15)
ON DUPLICATE KEY UPDATE username=VALUES(username);

-- 创建索引（优化查询性能）
CREATE INDEX idx_player_mmr ON players(mmr DESC);
CREATE INDEX idx_room_status ON rooms(status, mode);
CREATE INDEX idx_match_history_player ON match_history(player_id, created_at DESC);

-- 显示创建结果
SELECT '数据库初始化完成！' AS status;
SELECT COUNT(*) AS player_count FROM players;
SHOW TABLES;
