-- Expand the catalog for existing installations after enabling CosyVoice.
INSERT INTO supported_languages (
    language_code, display_name, display_name_en,
    supports_as_source, supports_as_target, sort_order, is_active
) VALUES
    ('ja-JP', '日本語', 'Japanese', TRUE, TRUE, 30, TRUE),
    ('ko-KR', '한국어', 'Korean', TRUE, TRUE, 40, TRUE),
    ('fr-FR', 'Français', 'French', TRUE, TRUE, 50, TRUE),
    ('de-DE', 'Deutsch', 'German', TRUE, TRUE, 60, TRUE),
    ('ru-RU', 'Русский', 'Russian', TRUE, TRUE, 70, TRUE),
    ('pt-BR', 'Português (Brasil)', 'Portuguese (Brazil)', TRUE, TRUE, 80, TRUE),
    ('th-TH', 'ไทย', 'Thai', TRUE, TRUE, 90, TRUE),
    ('id-ID', 'Bahasa Indonesia', 'Indonesian', TRUE, TRUE, 100, TRUE),
    ('vi-VN', 'Tiếng Việt', 'Vietnamese', TRUE, TRUE, 110, TRUE)
ON CONFLICT (language_code) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    display_name_en = EXCLUDED.display_name_en,
    supports_as_source = EXCLUDED.supports_as_source,
    supports_as_target = EXCLUDED.supports_as_target,
    sort_order = EXCLUDED.sort_order,
    is_active = EXCLUDED.is_active;
