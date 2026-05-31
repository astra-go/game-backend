-- 003_create_friend_tables.sql
-- 好友系统数据表

-- 好友关系表（双向存储）
CREATE TABLE IF NOT EXISTS friends (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    player_id VARCHAR(64) NOT NULL COMMENT '玩家ID',
    friend_id VARCHAR(64) NOT NULL COMMENT '好友ID',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP COMMENT '添加时间',
    INDEX idx_player_id (player_id),
    INDEX idx_friend_id (friend_id),
    UNIQUE KEY uk_player_friend (player_id, friend_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='好友关系表（双向存储）';

-- 好友请求表
CREATE TABLE IF NOT EXISTS friend_requests (
    id VARCHAR(64) PRIMARY KEY COMMENT '请求ID',
    player_id VARCHAR(64) NOT NULL COMMENT '发起者ID',
    target_id VARCHAR(64) NOT NULL COMMENT '目标玩家ID',
    status ENUM('pending', 'accepted', 'rejected') DEFAULT 'pending' COMMENT '请求状态',
    message VARCHAR(256) DEFAULT '' COMMENT '请求留言',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    expires_at TIMESTAMP NOT NULL COMMENT '过期时间（7天）',
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    INDEX idx_target_status (target_id, status),
    INDEX idx_player_status (player_id, status),
    INDEX idx_expires_at (expires_at),
    UNIQUE KEY uk_player_target (LEAST(player_id, target_id), GREATEST(player_id, target_id), status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='好友请求表';
