AGGKIT_REST_URL=${AGGKIT_URL:-localhost:5578}
AGGLAYER_PROTOS="/home/jesteban/.cache/buf/v3/modules/b5/buf.build/agglayer/agglayer/57743a879f16408a884b0d3484e7b0c2/files"
INTEROP_PROTOS="/home/jesteban/.cache/buf/v3/modules/b5/buf.build/agglayer/interop/85e8a3d9f59c4f9790789b45afb87c8e/files"
#"previous_certificate_id": {
#      "value": {
#        "value": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
#      }
#    },
grpcurl -plaintext \
  -import-path . \
  -import-path "$AGGLAYER_PROTOS" \
  -import-path "$INTEROP_PROTOS" \
  -proto aggsender/validator/proto/v1/validator.proto \
  -d '{
    
    "certificate": {
    "network_id": 1,
      "height": 42,
      "prev_local_exit_root": {
        "value": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
      },
      "new_local_exit_root": {
        "value": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
      },
      
    "metadata": {
        "value": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
      },
      "aggchain_data": {
       
      },
      "l1_info_tree_leaf_count": 500
    },
    "last_l2_block_in_cert": 1000
  }' \
  $AGGKIT_REST_URL \
  aggkit.aggsender.validator.v1.AggsenderValidator/ValidateCertificate

