import { useState, useEffect, useRef } from 'react';
import * as api from '../lib/api';

export default function ImagesPage() {
  const [groups, setGroups] = useState([]);
  const [selectedGroup, setSelectedGroup] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [deleteConfirm, setDeleteConfirm] = useState(null); // { type: 'single'|'group', data: ... }
  const [deleting, setDeleting] = useState(false);
  const [selections, setSelections] = useState(null); // Image selections tracking
  const galleryRef = useRef(null);
  const sectionRefs = useRef({});

  // Fetch image groups and selections
  const loadData = async () => {
    try {
      setLoading(true);
      const [imagesData, selectionsData] = await Promise.all([
        api.listImages(),
        api.getImageSelections(),
      ]);
      setGroups(imagesData);
      setSelections(selectionsData);
      setError(null);
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadData();
  }, []);

  // Get the selected filename for a group
  const getSelectedFilename = (groupType, groupName) => {
    if (!selections) return null;
    switch (groupType) {
      case 'article':
        return selections.articles?.[groupName] || null;
      case 'domain':
        return selections.indexes?.domains?.[groupName] || null;
      case 'category':
        return selections.indexes?.categories?.[groupName] || null;
      case 'topic':
        return selections.indexes?.topics?.[groupName] || null;
      default:
        return null;
    }
  };

  // Handle image selection
  const handleSelectImage = async (groupType, groupName, filename) => {
    try {
      await api.updateImageSelection(groupType, groupName, filename);
      // Update local state
      setSelections(prev => {
        const updated = { ...prev };
        switch (groupType) {
          case 'article':
            updated.articles = { ...updated.articles, [groupName]: filename };
            break;
          case 'domain':
            updated.indexes = { ...updated.indexes, domains: { ...updated.indexes?.domains, [groupName]: filename } };
            break;
          case 'category':
            updated.indexes = { ...updated.indexes, categories: { ...updated.indexes?.categories, [groupName]: filename } };
            break;
          case 'topic':
            updated.indexes = { ...updated.indexes, topics: { ...updated.indexes?.topics, [groupName]: filename } };
            break;
        }
        return updated;
      });
    } catch (e) {
      setError(`Failed to save selection: ${e.message}`);
    }
  };

  // Legacy function name for compatibility
  const loadImages = loadData;

  // Scroll to section when clicking left panel
  const scrollToSection = (groupKey) => {
    setSelectedGroup(groupKey);
    const element = sectionRefs.current[groupKey];
    if (element) {
      element.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }
  };

  // Delete handlers
  const handleDeleteSingle = async (path) => {
    setDeleting(true);
    try {
      await api.deleteImage(path);
      await loadImages();
      setDeleteConfirm(null);
    } catch (e) {
      setError(`Failed to delete image: ${e.message}`);
    } finally {
      setDeleting(false);
    }
  };

  const handleDeleteGroup = async (type, name) => {
    setDeleting(true);
    try {
      await api.deleteImageGroup(type, name);
      await loadImages();
      setDeleteConfirm(null);
    } catch (e) {
      setError(`Failed to delete group: ${e.message}`);
    } finally {
      setDeleting(false);
    }
  };

  // Group by type for display
  const groupedByType = {
    domain: groups.filter(g => g.type === 'domain'),
    category: groups.filter(g => g.type === 'category'),
    topic: groups.filter(g => g.type === 'topic'),
    article: groups.filter(g => g.type === 'article'),
  };

  const typeLabels = {
    domain: 'Domain Indexes',
    category: 'Category Indexes',
    topic: 'Topic Indexes',
    article: 'Articles',
  };

  const typeColors = {
    domain: 'var(--accent-blue)',
    category: 'var(--accent-purple)',
    topic: 'var(--accent-green)',
    article: 'var(--text-secondary)',
  };

  return (
    <div style={{ height: 'calc(100vh - 48px)', display: 'flex', flexDirection: 'column' }}>
      <div style={{ marginBottom: 20 }}>
        <h1 style={{
          fontSize: '1.75rem',
          fontWeight: 700,
          letterSpacing: '-0.02em',
        }}>
          Image Generation
        </h1>
        <p style={{
          color: 'var(--text-secondary)',
          fontSize: '0.9rem',
          marginTop: 4,
        }}>
          Browse and manage generated images from _incoming directory
        </p>
      </div>

      {error && (
        <div style={{
          padding: '12px 16px',
          backgroundColor: 'rgba(239, 68, 68, 0.1)',
          border: '1px solid var(--accent-red)',
          borderRadius: 8,
          marginBottom: 20,
          color: 'var(--accent-red)',
          fontSize: '0.9rem',
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
        }}>
          <span>{error}</span>
          <button 
            onClick={() => setError(null)}
            style={{
              background: 'none',
              border: 'none',
              color: 'var(--accent-red)',
              cursor: 'pointer',
              fontSize: '1.2rem',
              padding: '0 4px',
            }}
          >
            ×
          </button>
        </div>
      )}

      <div style={{
        flex: 1,
        display: 'grid',
        gridTemplateColumns: '260px 1fr',
        gap: 20,
        minHeight: 0,
      }}>
        {/* Navigation List */}
        <div style={{
          backgroundColor: 'var(--bg-card)',
          border: '1px solid var(--border-color)',
          borderRadius: 12,
          overflow: 'hidden',
          display: 'flex',
          flexDirection: 'column',
        }}>
          <div style={{
            padding: '12px 16px',
            borderBottom: '1px solid var(--border-color)',
            fontSize: '0.85rem',
            fontWeight: 600,
            color: 'var(--text-secondary)',
          }}>
            Image Groups ({groups.length})
          </div>
          
          <div style={{
            flex: 1,
            overflowY: 'auto',
          }}>
            {loading ? (
              <div style={{ padding: 16, color: 'var(--text-muted)' }}>
                Loading...
              </div>
            ) : groups.length === 0 ? (
              <div style={{ padding: 16, color: 'var(--text-muted)' }}>
                No images found in _incoming
              </div>
            ) : (
              Object.entries(groupedByType).map(([type, typeGroups]) => (
                typeGroups.length > 0 && (
                  <div key={type}>
                    <div style={{
                      padding: '8px 16px',
                      fontSize: '0.75rem',
                      fontWeight: 600,
                      color: typeColors[type],
                      backgroundColor: 'var(--bg-secondary)',
                      textTransform: 'uppercase',
                      letterSpacing: '0.05em',
                    }}>
                      {typeLabels[type]} ({typeGroups.length})
                    </div>
                    {typeGroups.map(group => {
                      const groupKey = `${group.type}-${group.name}`;
                      return (
                        <button
                          key={groupKey}
                          onClick={() => scrollToSection(groupKey)}
                          style={{
                            width: '100%',
                            padding: '10px 16px',
                            textAlign: 'left',
                            border: 'none',
                            backgroundColor: selectedGroup === groupKey 
                              ? 'var(--bg-hover)' 
                              : 'transparent',
                            borderLeft: selectedGroup === groupKey 
                              ? `3px solid ${typeColors[type]}` 
                              : '3px solid transparent',
                            cursor: 'pointer',
                            transition: 'all 0.15s ease',
                          }}
                        >
                          <div style={{
                            fontSize: '0.85rem',
                            fontWeight: selectedGroup === groupKey ? 500 : 400,
                            color: 'var(--text-primary)',
                          }}>
                            {group.name.replace(/--/g, ' → ')}
                          </div>
                          <div style={{
                            fontSize: '0.75rem',
                            color: 'var(--text-muted)',
                            marginTop: 2,
                          }}>
                            {group.images.length} image{group.images.length !== 1 ? 's' : ''}
                          </div>
                        </button>
                      );
                    })}
                  </div>
                )
              ))
            )}
          </div>
        </div>

        {/* Image Gallery */}
        <div 
          ref={galleryRef}
          style={{
            backgroundColor: 'var(--bg-card)',
            border: '1px solid var(--border-color)',
            borderRadius: 12,
            overflow: 'hidden',
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          <div style={{
            flex: 1,
            overflowY: 'auto',
            padding: 20,
          }}>
            {loading ? (
              <div style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                height: '100%',
                color: 'var(--text-muted)',
              }}>
                Loading...
              </div>
            ) : groups.length === 0 ? (
              <div style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                height: '100%',
                color: 'var(--text-muted)',
              }}>
                No images found
              </div>
            ) : (
              Object.entries(groupedByType).map(([type, typeGroups]) => (
                typeGroups.length > 0 && (
                  <div key={type} style={{ marginBottom: 32 }}>
                    <h2 style={{
                      fontSize: '1.1rem',
                      fontWeight: 600,
                      color: typeColors[type],
                      marginBottom: 16,
                      paddingBottom: 8,
                      borderBottom: `2px solid ${typeColors[type]}`,
                    }}>
                      {typeLabels[type]}
                    </h2>
                    
                    {typeGroups.map(group => {
                      const groupKey = `${group.type}-${group.name}`;
                      return (
                        <div 
                          key={groupKey}
                          ref={el => sectionRefs.current[groupKey] = el}
                          style={{ marginBottom: 24 }}
                        >
                          <div style={{
                            display: 'flex',
                            justifyContent: 'space-between',
                            alignItems: 'center',
                            marginBottom: 12,
                          }}>
                            <h3 style={{
                              fontSize: '0.95rem',
                              fontWeight: 500,
                              color: 'var(--text-primary)',
                              margin: 0,
                            }}>
                              {group.name.replace(/--/g, ' → ')}
                            </h3>
                            <button
                              onClick={() => setDeleteConfirm({ 
                                type: 'group', 
                                groupType: group.type, 
                                name: group.name,
                                count: group.images.length 
                              })}
                              style={{
                                padding: '4px 10px',
                                fontSize: '0.75rem',
                                backgroundColor: 'transparent',
                                border: '1px solid var(--accent-red)',
                                borderRadius: 4,
                                color: 'var(--accent-red)',
                                cursor: 'pointer',
                                transition: 'all 0.15s ease',
                              }}
                              onMouseOver={(e) => {
                                e.target.style.backgroundColor = 'rgba(239, 68, 68, 0.1)';
                              }}
                              onMouseOut={(e) => {
                                e.target.style.backgroundColor = 'transparent';
                              }}
                            >
                              Delete All ({group.images.length})
                            </button>
                          </div>
                          
                          <div style={{
                            display: 'grid',
                            gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))',
                            gap: 16,
                          }}>
                            {group.images.map(image => {
                              const imagePath = group.path 
                                ? `${group.path}/${image}` 
                                : image;
                              const selectedFilename = getSelectedFilename(group.type, group.name);
                              const isSelected = selectedFilename === image;
                              const hasSelection = selectedFilename !== null;
                              return (
                                <ImageCard 
                                  key={image}
                                  filename={image}
                                  path={imagePath}
                                  isSelected={isSelected}
                                  hasGroupSelection={hasSelection}
                                  onSelect={() => handleSelectImage(group.type, group.name, image)}
                                  onDelete={() => setDeleteConfirm({ 
                                    type: 'single', 
                                    path: imagePath, 
                                    filename: image 
                                  })}
                                />
                              );
                            })}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )
              ))
            )}
          </div>
        </div>
      </div>

      {/* Delete Confirmation Modal */}
      {deleteConfirm && (
        <div style={{
          position: 'fixed',
          inset: 0,
          backgroundColor: 'rgba(0, 0, 0, 0.6)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          zIndex: 1000,
        }}>
          <div style={{
            backgroundColor: 'var(--bg-card)',
            borderRadius: 12,
            padding: 24,
            maxWidth: 400,
            width: '90%',
            border: '1px solid var(--border-color)',
          }}>
            <h3 style={{
              fontSize: '1.1rem',
              fontWeight: 600,
              marginBottom: 12,
              color: 'var(--text-primary)',
            }}>
              Confirm Delete
            </h3>
            <p style={{
              color: 'var(--text-secondary)',
              fontSize: '0.9rem',
              marginBottom: 20,
              lineHeight: 1.5,
            }}>
              {deleteConfirm.type === 'single' 
                ? `Are you sure you want to delete "${deleteConfirm.filename}"? This will also delete the corresponding prompt file, allowing regeneration on next backfill.`
                : `Are you sure you want to delete all ${deleteConfirm.count} images for "${deleteConfirm.name.replace(/--/g, ' → ')}"? This will also delete all corresponding prompt files.`
              }
            </p>
            <div style={{
              display: 'flex',
              gap: 12,
              justifyContent: 'flex-end',
            }}>
              <button
                onClick={() => setDeleteConfirm(null)}
                disabled={deleting}
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--bg-secondary)',
                  border: '1px solid var(--border-color)',
                  borderRadius: 6,
                  color: 'var(--text-primary)',
                  cursor: deleting ? 'not-allowed' : 'pointer',
                  opacity: deleting ? 0.5 : 1,
                }}
              >
                Cancel
              </button>
              <button
                onClick={() => {
                  if (deleteConfirm.type === 'single') {
                    handleDeleteSingle(deleteConfirm.path);
                  } else {
                    handleDeleteGroup(deleteConfirm.groupType, deleteConfirm.name);
                  }
                }}
                disabled={deleting}
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--accent-red)',
                  border: 'none',
                  borderRadius: 6,
                  color: 'white',
                  cursor: deleting ? 'not-allowed' : 'pointer',
                  opacity: deleting ? 0.5 : 1,
                }}
              >
                {deleting ? 'Deleting...' : 'Delete'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function ImageCard({ filename, path, isSelected, hasGroupSelection, onSelect, onDelete }) {
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState(false);
  const [showActions, setShowActions] = useState(false);

  const imageUrl = api.getImageUrl(path);
  
  // Determine opacity: full if selected or no selection made, dimmed if not selected but group has a selection
  const cardOpacity = hasGroupSelection && !isSelected ? 0.4 : 1;

  return (
    <div 
      style={{
        backgroundColor: 'var(--bg-secondary)',
        borderRadius: 8,
        overflow: 'hidden',
        border: isSelected ? '3px solid var(--accent-green)' : '1px solid var(--border-color)',
        position: 'relative',
        opacity: cardOpacity,
        transition: 'opacity 0.2s ease, border 0.2s ease',
      }}
      onMouseEnter={() => setShowActions(true)}
      onMouseLeave={() => setShowActions(false)}
    >
      {/* Selected badge */}
      {isSelected && (
        <div style={{
          position: 'absolute',
          top: 8,
          left: 8,
          zIndex: 10,
          backgroundColor: 'var(--accent-green)',
          color: 'white',
          padding: '4px 8px',
          borderRadius: 4,
          fontSize: '0.7rem',
          fontWeight: 600,
          display: 'flex',
          alignItems: 'center',
          gap: 4,
        }}>
          <span>✓</span> Selected
        </div>
      )}
      
      <div style={{
        aspectRatio: '16/9',
        position: 'relative',
        backgroundColor: 'var(--bg-primary)',
        cursor: 'pointer',
      }}
      onClick={onSelect}
      >
        {!loaded && !error && (
          <div style={{
            position: 'absolute',
            inset: 0,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            color: 'var(--text-muted)',
            fontSize: '0.8rem',
          }}>
            Loading...
          </div>
        )}
        {error ? (
          <div style={{
            position: 'absolute',
            inset: 0,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            color: 'var(--accent-red)',
            fontSize: '0.8rem',
          }}>
            Failed to load
          </div>
        ) : (
          <img
            src={imageUrl}
            alt={filename}
            onLoad={() => setLoaded(true)}
            onError={() => setError(true)}
            style={{
              width: '100%',
              height: '100%',
              objectFit: 'cover',
              opacity: loaded ? 1 : 0,
              transition: 'opacity 0.3s ease',
            }}
          />
        )}
        
        {/* Action overlay */}
        {showActions && loaded && (
          <div style={{
            position: 'absolute',
            top: 8,
            right: 8,
            display: 'flex',
            gap: 6,
          }}>
            <button
              onClick={(e) => {
                e.stopPropagation();
                window.open(imageUrl, '_blank');
              }}
              style={{
                width: 32,
                height: 32,
                borderRadius: 6,
                backgroundColor: 'rgba(0, 0, 0, 0.7)',
                border: 'none',
                color: 'white',
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                fontSize: '1rem',
              }}
              title="Open in new tab"
            >
              ↗
            </button>
            <button
              onClick={(e) => {
                e.stopPropagation();
                onDelete();
              }}
              style={{
                width: 32,
                height: 32,
                borderRadius: 6,
                backgroundColor: 'rgba(239, 68, 68, 0.9)',
                border: 'none',
                color: 'white',
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                fontSize: '1rem',
              }}
              title="Delete image"
            >
              🗑
            </button>
          </div>
        )}
        
        {/* Click to select hint on hover when not selected */}
        {showActions && loaded && !isSelected && (
          <div style={{
            position: 'absolute',
            bottom: 8,
            left: '50%',
            transform: 'translateX(-50%)',
            backgroundColor: 'rgba(0, 0, 0, 0.7)',
            color: 'white',
            padding: '4px 12px',
            borderRadius: 4,
            fontSize: '0.75rem',
            whiteSpace: 'nowrap',
          }}>
            Click to select
          </div>
        )}
      </div>
      <div style={{
        padding: '8px 12px',
        fontSize: '0.75rem',
        color: isSelected ? 'var(--accent-green)' : 'var(--text-muted)',
        fontFamily: 'JetBrains Mono, monospace',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        fontWeight: isSelected ? 600 : 400,
      }}>
        {filename}
      </div>
    </div>
  );
}
