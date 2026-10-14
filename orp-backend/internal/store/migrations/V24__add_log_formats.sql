ALTER TABLE http_configuration
    ADD COLUMN http_log_format VARCHAR(4096) NOT NULL DEFAULT 'orp ''$remote_addr - $remote_user [$time_local] "$request" $status $body_bytes_sent''',
    ADD COLUMN stream_log_format VARCHAR(4096) NOT NULL DEFAULT 'orp_stream ''$remote_addr [$time_local] $protocol $status $bytes_sent $bytes_received $session_time''';
